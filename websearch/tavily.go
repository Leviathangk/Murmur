package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Qsnh/Murmur/locales"
)

type Result struct {
	Title     string
	URL       string
	Content   string
	Published string
}

const (
	TavilyURL      = "https://api.tavily.com/search"
	maxResults     = 5
	maxContent     = 2000
	attemptTimeout = 20 * time.Second
	maxRetryWait   = 5 * time.Second
)

type Tavily struct {
	Key    string
	Client *http.Client
	URL    string
	wait   func(context.Context, time.Duration) error
}

type tavilyResponse struct {
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Content       string `json:"content"`
		PublishedDate string `json:"published_date"`
	} `json:"results"`
}

func (t Tavily) Search(ctx context.Context, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	body := map[string]any{
		"query":                  query,
		"search_depth":           "basic",
		"max_results":            maxResults,
		"include_answer":         false,
		"include_raw_content":    false,
		"include_published_date": true,
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var resp *tavilyResponse
		var retry time.Duration
		resp, retry, err = t.post(ctx, body)
		if err == nil {
			return results(resp), nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var se *statusError
		if errors.As(err, &se) && (se.status == http.StatusBadRequest || se.status == http.StatusUnprocessableEntity) {
			if _, ok := body["include_published_date"]; !ok {
				return nil, err
			}
			body = map[string]any{"query": query, "max_results": maxResults}
			continue
		}
		if retry < 0 || attempt > 0 {
			return nil, err
		}
		if err := t.sleep(ctx, retry); err != nil {
			return nil, err
		}
	}
	return nil, err
}

func (t Tavily) sleep(ctx context.Context, d time.Duration) error {
	if t.wait != nil {
		return t.wait(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

func (t Tavily) post(ctx context.Context, body map[string]any) (*tavilyResponse, time.Duration, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, -1, err
	}
	url := t.URL
	if url == "" {
		url = TavilyURL
	}
	actx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(t.Key))
	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, -1, ctx.Err()
		}
		if actx.Err() != nil {
			return nil, time.Second, errors.New(locales.TavilyTimeout())
		}
		return nil, time.Second, errors.New(locales.TavilyUnreachable(err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		var out tavilyResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			if actx.Err() != nil && ctx.Err() == nil {
				return nil, time.Second, errors.New(locales.TavilyTimeout())
			}
			return nil, time.Second, errors.New(locales.TavilyHTTPError(strconv.Itoa(resp.StatusCode), err.Error()))
		}
		return &out, 0, nil
	}
	msg := errorMessage(resp)
	se := &statusError{status: resp.StatusCode}
	retry := time.Duration(-1)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		se.msg = locales.TavilyBadKey()
	case resp.StatusCode == 432 || resp.StatusCode == 433:
		se.msg = locales.TavilyQuota()
	case resp.StatusCode == http.StatusTooManyRequests:
		se.msg = locales.TavilyRateLimited()
		retry = retryAfter(resp.Header.Get("Retry-After"))
	default:
		se.msg = locales.TavilyHTTPError(strconv.Itoa(resp.StatusCode), msg)
		if resp.StatusCode >= 500 {
			retry = time.Second
		}
	}
	return nil, retry, se
}

func retryAfter(h string) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && s >= 0 {
		return min(time.Duration(s)*time.Second, maxRetryWait)
	}
	return time.Second
}

func errorMessage(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var e struct {
		Detail json.RawMessage `json:"detail"`
		Error  string          `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil {
		var inner struct {
			Error string `json:"error"`
		}
		var s string
		switch {
		case json.Unmarshal(e.Detail, &inner) == nil && inner.Error != "":
			return inner.Error
		case json.Unmarshal(e.Detail, &s) == nil && s != "":
			return s
		case e.Error != "":
			return e.Error
		case len(e.Detail) > 0:
			return clip(string(e.Detail), 300)
		}
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		return clip(s, 300)
	}
	return http.StatusText(resp.StatusCode)
}

func results(resp *tavilyResponse) []Result {
	var out []Result
	seen := map[string]bool{}
	for _, r := range resp.Results {
		u := strings.TrimSpace(r.URL)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		title := strings.Join(strings.Fields(r.Title), " ")
		if title == "" {
			title = u
		}
		out = append(out, Result{
			Title:     title,
			URL:       u,
			Content:   clip(strings.TrimSpace(r.Content), maxContent),
			Published: strings.TrimSpace(r.PublishedDate),
		})
	}
	return out
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

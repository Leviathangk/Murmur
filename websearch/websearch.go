package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
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

type Page struct {
	URL       string
	Title     string
	Text      string
	Published string
}

type Service interface {
	Search(ctx context.Context, query string) ([]Result, error)
	Read(ctx context.Context, url string) (*Page, error)
}

const (
	maxContent     = 2000
	attemptTimeout = 20 * time.Second
	maxRetryWait   = 5 * time.Second
)

var readTimeout = 60 * time.Second

type api struct {
	name   string
	client *http.Client
	wait   func(context.Context, time.Duration) error
	codes  map[int]string
}

type request struct {
	method string
	url    string
	header http.Header
	body   any
}

type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

type timeoutError struct{ msg string }

func (e *timeoutError) Error() string { return e.msg }

func (a api) retry(ctx context.Context, try func() (time.Duration, error)) error {
	wait, err := try()
	if err != nil && wait >= 0 && ctx.Err() == nil {
		if err := a.sleep(ctx, wait); err != nil {
			return err
		}
		_, err = try()
	}
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (a api) read(ctx context.Context, r request, out any) error {
	return a.retry(ctx, func() (time.Duration, error) {
		wait, err := a.send(ctx, readTimeout, r, out)
		var te *timeoutError
		if errors.As(err, &te) {
			return -1, err
		}
		return wait, err
	})
}

func (a api) sleep(ctx context.Context, d time.Duration) error {
	if a.wait != nil {
		return a.wait(ctx, d)
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

func (a api) send(ctx context.Context, timeout time.Duration, r request, out any) (time.Duration, error) {
	var body io.Reader
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return -1, err
		}
		body = bytes.NewReader(b)
	}
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, r.method, r.url, body)
	if err != nil {
		return -1, err
	}
	for k, v := range r.header {
		req.Header[k] = v
	}
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := a.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		if actx.Err() != nil {
			return time.Second, &timeoutError{locales.WebTimeout(a.name)}
		}
		return time.Second, errors.New(locales.WebUnreachable(a.name, err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		reflect.ValueOf(out).Elem().SetZero()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			if ctx.Err() != nil {
				return -1, ctx.Err()
			}
			if actx.Err() != nil {
				return time.Second, &timeoutError{locales.WebTimeout(a.name)}
			}
			return time.Second, errors.New(locales.WebHTTPError(a.name, strconv.Itoa(resp.StatusCode), err.Error()))
		}
		return 0, nil
	}
	se := &statusError{status: resp.StatusCode}
	retry := time.Duration(-1)
	if msg, ok := a.codes[resp.StatusCode]; ok {
		se.msg = msg
	} else if resp.StatusCode == http.StatusTooManyRequests {
		se.msg = locales.WebRateLimited(a.name)
		retry = retryAfter(resp.Header.Get("Retry-After"))
	} else {
		se.msg = locales.WebHTTPError(a.name, strconv.Itoa(resp.StatusCode), errorMessage(resp))
		if resp.StatusCode >= 500 {
			retry = time.Second
		}
	}
	return retry, se
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
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil {
		var inner struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		var s string
		switch {
		case json.Unmarshal(e.Detail, &inner) == nil && inner.Error != "":
			return inner.Error
		case json.Unmarshal(e.Detail, &s) == nil && s != "":
			return s
		case json.Unmarshal(e.Error, &s) == nil && s != "":
			return s
		case json.Unmarshal(e.Error, &inner) == nil && inner.Message != "":
			return inner.Message
		case len(e.Detail) > 0:
			return clip(string(e.Detail), 300)
		}
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		return clip(s, 300)
	}
	return http.StatusText(resp.StatusCode)
}

func clean(results []Result) []Result {
	var out []Result
	seen := map[string]bool{}
	for _, r := range results {
		u := strings.TrimSpace(r.URL)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		title := oneLine(r.Title)
		if title == "" {
			title = u
		}
		out = append(out, Result{
			Title:     title,
			URL:       u,
			Content:   clip(strings.TrimSpace(r.Content), maxContent),
			Published: strings.TrimSpace(r.Published),
		})
	}
	return out
}

func page(link, title, text, published string) (*Page, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New(locales.ReadEmpty())
	}
	return &Page{URL: link, Title: oneLine(title), Text: text, Published: strings.TrimSpace(published)}, nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

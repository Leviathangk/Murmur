package websearch

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Qsnh/Murmur/locales"
)

const (
	TavilySearchURL  = "https://api.tavily.com/search"
	TavilyExtractURL = "https://api.tavily.com/extract"
	maxResults       = 5
)

type Tavily struct {
	Key        string
	Client     *http.Client
	SearchURL  string
	ExtractURL string
	wait       func(context.Context, time.Duration) error
}

type tavilyResponse struct {
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Content       string `json:"content"`
		PublishedDate string `json:"published_date"`
	} `json:"results"`
}

type tavilyExtract struct {
	Results []struct {
		Title      string `json:"title"`
		RawContent string `json:"raw_content"`
	} `json:"results"`
	FailedResults []struct {
		Error string `json:"error"`
	} `json:"failed_results"`
}

func (t Tavily) api() api {
	return api{name: "Tavily", client: t.Client, wait: t.wait, codes: map[int]string{
		http.StatusUnauthorized: locales.WebBadKey("Tavily"),
		http.StatusForbidden:    locales.WebBadKey("Tavily"),
		432:                     locales.WebQuota("Tavily"),
		433:                     locales.WebQuota("Tavily"),
	}}
}

func (t Tavily) request(url, fallback string, body any) request {
	if url == "" {
		url = fallback
	}
	return request{method: http.MethodPost, url: url, body: body, header: http.Header{"Authorization": {"Bearer " + strings.TrimSpace(t.Key)}}}
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
	a := t.api()
	var out tavilyResponse
	err := a.retry(ctx, func() (time.Duration, error) {
		wait, err := a.send(ctx, attemptTimeout, t.request(t.SearchURL, TavilySearchURL, body), &out)
		var se *statusError
		if errors.As(err, &se) && (se.status == http.StatusBadRequest || se.status == http.StatusUnprocessableEntity) {
			if _, ok := body["include_published_date"]; ok {
				body = map[string]any{"query": query, "max_results": maxResults}
				_, err := a.send(ctx, attemptTimeout, t.request(t.SearchURL, TavilySearchURL, body), &out)
				return -1, err
			}
		}
		return wait, err
	})
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(out.Results))
	for i, r := range out.Results {
		results[i] = Result{Title: r.Title, URL: r.URL, Content: r.Content, Published: r.PublishedDate}
	}
	return clean(results), nil
}

func (t Tavily) Read(ctx context.Context, link string) (*Page, error) {
	body := map[string]any{"urls": []string{link}, "extract_depth": "basic", "format": "markdown"}
	a := t.api()
	var out tavilyExtract
	err := a.read(ctx, t.request(t.ExtractURL, TavilyExtractURL, body), &out)
	switch {
	case err != nil:
		return nil, err
	case len(out.Results) > 0:
		r := out.Results[0]
		return page(link, r.Title, r.RawContent, "")
	case len(out.FailedResults) > 0 && strings.TrimSpace(out.FailedResults[0].Error) != "":
		return nil, errors.New(locales.ReadFailed(strings.TrimSpace(out.FailedResults[0].Error)))
	}
	return nil, errors.New(locales.ReadEmpty())
}

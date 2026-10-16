package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Qsnh/Murmur/locales"
)

const (
	TinyFishSearchURL = "https://api.search.tinyfish.ai"
	TinyFishFetchURL  = "https://api.fetch.tinyfish.ai"
	pageTimeoutMS     = 45000
)

type TinyFish struct {
	Key       string
	Client    *http.Client
	SearchURL string
	FetchURL  string
	wait      func(context.Context, time.Duration) error
}

type tinyFishResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
		Date    string `json:"date"`
	} `json:"results"`
}

type tinyFishFetch struct {
	Results []struct {
		Title         string `json:"title"`
		Text          string `json:"text"`
		PublishedDate string `json:"published_date"`
	} `json:"results"`
	Errors []struct {
		Error  string `json:"error"`
		Status int    `json:"status"`
	} `json:"errors"`
}

func (t TinyFish) api() api {
	return api{name: "TinyFish", client: t.Client, wait: t.wait, codes: map[int]string{
		http.StatusUnauthorized:    locales.WebBadKey("TinyFish"),
		http.StatusPaymentRequired: locales.WebDailyQuota("TinyFish"),
	}}
}

func (t TinyFish) header() http.Header {
	return http.Header{"X-Api-Key": {strings.TrimSpace(t.Key)}}
}

func (t TinyFish) Search(ctx context.Context, query string) ([]Result, error) {
	base := t.SearchURL
	if base == "" {
		base = TinyFishSearchURL
	}
	r := request{method: http.MethodGet, url: base + "?" + url.Values{"query": {strings.TrimSpace(query)}}.Encode(), header: t.header()}
	a := t.api()
	var out tinyFishResponse
	err := a.retry(ctx, func() (time.Duration, error) {
		return a.send(ctx, attemptTimeout, r, &out)
	})
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(out.Results))
	for i, r := range out.Results {
		results[i] = Result{Title: r.Title, URL: r.URL, Content: r.Snippet, Published: r.Date}
	}
	return clean(results), nil
}

func (t TinyFish) Read(ctx context.Context, link string) (*Page, error) {
	u := t.FetchURL
	if u == "" {
		u = TinyFishFetchURL
	}
	r := request{method: http.MethodPost, url: u, header: t.header(), body: map[string]any{
		"urls":               []string{link},
		"format":             "markdown",
		"per_url_timeout_ms": pageTimeoutMS,
	}}
	a := t.api()
	var out tinyFishFetch
	err := a.read(ctx, r, &out)
	switch {
	case err != nil:
		return nil, err
	case len(out.Results) > 0:
		r := out.Results[0]
		return page(link, r.Title, r.Text, r.PublishedDate)
	case len(out.Errors) > 0:
		return nil, errors.New(fetchError(out.Errors[0].Error, out.Errors[0].Status))
	}
	return nil, errors.New(locales.ReadEmpty())
}

func fetchError(code string, status int) string {
	switch code {
	case "page_not_found":
		return locales.ReadNotFound()
	case "bot_blocked":
		return locales.ReadBlocked()
	case "login_required":
		return locales.ReadLogin()
	case "timeout":
		return locales.ReadTimeout()
	case "target_unreachable", "proxy_error":
		return locales.ReadUnreachable()
	case "empty_content":
		return locales.ReadEmpty()
	case "content_too_large":
		return locales.ReadTooLarge()
	case "invalid_url", "invalid_redirect_url":
		return locales.ReadInvalidURL()
	case "target_http_error":
		if status > 0 {
			return locales.ReadHTTPError(strconv.Itoa(status))
		}
	}
	return locales.ReadFailed(code)
}

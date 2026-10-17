package websearch

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Qsnh/Murmur/locales"
)

const (
	DeepSeekSearchURL = "https://api.deepseek.com/v1/tools/search"
	DeepSeekFetchURL  = "https://api.deepseek.com/v1/tools/fetch"
	maxDeepSeekQuery  = 128
	markdownPunct     = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
)

type DeepSeek struct {
	Key       string
	Client    *http.Client
	SearchURL string
	FetchURL  string
	wait      func(context.Context, time.Duration) error
}

type deepSeekResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
		Summary string `json:"summary"`
		Date    string `json:"date"`
	} `json:"results"`
}

type deepSeekFetch struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (d DeepSeek) api(read bool) api {
	codes := map[int]string{
		http.StatusUnauthorized:    locales.WebBadKey("DeepSeek"),
		http.StatusPaymentRequired: locales.WebNoBalance("DeepSeek"),
	}
	if read {
		codes[http.StatusForbidden] = locales.ReadRefused("DeepSeek")
		codes[http.StatusNotFound] = locales.ReadUnavailable()
		codes[http.StatusGatewayTimeout] = locales.ReadTimeout()
	}
	return api{name: "DeepSeek", client: d.Client, wait: d.wait, codes: codes}
}

func (d DeepSeek) request(url, fallback string, body any) request {
	if url == "" {
		url = fallback
	}
	return request{method: http.MethodPost, url: url, body: body, header: http.Header{"Authorization": {"Bearer " + strings.TrimSpace(d.Key)}}}
}

func (d DeepSeek) Search(ctx context.Context, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) > maxDeepSeekQuery {
		query = strings.TrimSpace(string([]rune(query)[:maxDeepSeekQuery]))
	}
	r := d.request(d.SearchURL, DeepSeekSearchURL, map[string]any{"query": query, "count": maxResults})
	a := d.api(false)
	var out deepSeekResponse
	err := a.retry(ctx, func() (time.Duration, error) {
		return a.send(ctx, attemptTimeout, r, &out)
	})
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(out.Results))
	for i, r := range out.Results {
		content := r.Summary
		if strings.TrimSpace(content) == "" {
			content = r.Snippet
		}
		link, _, _ := strings.Cut(r.URL, "#")
		results[i] = Result{Title: r.Title, URL: link, Content: content, Published: publishDate(r.Date)}
	}
	return clean(results), nil
}

func (d DeepSeek) Read(ctx context.Context, link string) (*Page, error) {
	a := d.api(true)
	var out deepSeekFetch
	if err := a.read(ctx, d.request(d.FetchURL, DeepSeekFetchURL, map[string]any{"url": link}), &out); err != nil {
		return nil, err
	}
	return page(link, out.Title, unescapeMarkdown(out.Content), "")
}

var chinaTime = time.FixedZone("UTC+8", 8*3600)

func publishDate(s string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return s
	}
	t = t.In(chinaTime)
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
		return t.Format(time.DateOnly)
	}
	return t.Format(time.RFC3339)
}

func unescapeMarkdown(s string) string {
	escaped, plain := 0, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && strings.IndexByte(markdownPunct, s[i+1]) >= 0:
			escaped++
			i++
		case strings.IndexByte(markdownPunct, s[i]) >= 0:
			plain++
		}
	}
	if escaped <= plain {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(markdownPunct, s[i+1]) >= 0 {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

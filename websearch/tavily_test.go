package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Qsnh/Murmur/locales"
)

type call struct {
	auth string
	path string
	body map[string]any
}

func tavily(t *testing.T, replies ...func(w http.ResponseWriter)) (Tavily, *[]call, *[]time.Duration) {
	t.Helper()
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, call{r.Header.Get("Authorization"), r.URL.Path, body})
		replies[min(len(calls), len(replies))-1](w)
	}))
	t.Cleanup(srv.Close)
	var waits []time.Duration
	tv := Tavily{Key: " tvly-test ", SearchURL: srv.URL + "/search", ExtractURL: srv.URL + "/extract", wait: func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}}
	return tv, &calls, &waits
}

func status(code int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

const found = `{"query":"go","results":[
	{"title":"The Go\n Programming Language","url":"https://go.dev/","content":"Go is an open source language.","score":0.9,"published_date":"2026-10-01"},
	{"title":"Go again","url":"https://go.dev/","content":"dup","score":0.5},
	{"title":"","url":"https://example.com/a","content":"untitled","score":0.4,"published_date":null},
	{"title":"no url","url":"","content":"x"}
],"response_time":1.2}`

func TestSearch(t *testing.T) {
	tv, calls, _ := tavily(t, status(200, found))
	got, err := tv.Search(context.Background(), "  go  ")
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{Title: "The Go Programming Language", URL: "https://go.dev/", Content: "Go is an open source language.", Published: "2026-10-01"},
		{Title: "https://example.com/a", URL: "https://example.com/a", Content: "untitled"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("results %+v\nwant %+v", got, want)
	}
	c := (*calls)[0]
	if c.auth != "Bearer tvly-test" || c.path != "/search" {
		t.Errorf("authorization %q, path %q", c.auth, c.path)
	}
	b, _ := json.Marshal(c.body)
	if string(b) != `{"include_answer":false,"include_published_date":true,"include_raw_content":false,"max_results":5,"query":"go","search_depth":"basic"}` {
		t.Errorf("body %s", b)
	}
}

func TestSearchClipsContent(t *testing.T) {
	long := strings.Repeat("字", maxContent+10)
	tv, _, _ := tavily(t, status(200, `{"results":[{"title":"t","url":"https://a.b/","content":"`+long+`"}]}`))
	got, err := tv.Search(context.Background(), "q")
	if err != nil || len(got) != 1 || got[0].Content != strings.Repeat("字", maxContent)+"…" {
		t.Errorf("results %d, error %v", len(got), err)
	}
}

func TestSearchErrors(t *testing.T) {
	cases := []struct {
		reply func(w http.ResponseWriter)
		want  string
		calls int
	}{
		{status(401, `{"detail":{"error":"Unauthorized: missing or invalid API key."}}`), locales.WebBadKey("Tavily"), 1},
		{status(432, `{"detail":{"error":"This request exceeds your plan's set usage limit."}}`), locales.WebQuota("Tavily"), 1},
		{status(433, `{"detail":{"error":"pay-as-you-go limit"}}`), locales.WebQuota("Tavily"), 1},
		{status(500, `{"detail":{"error":"boom"}}`), locales.WebHTTPError("Tavily", "500", "boom"), 2},
		{status(502, `<html>bad gateway</html>`), locales.WebHTTPError("Tavily", "502", "<html>bad gateway</html>"), 2},
		{status(429, `{"detail":{"error":"slow down"}}`), locales.WebRateLimited("Tavily"), 2},
		{status(200, `not json`), "", 2},
	}
	for _, c := range cases {
		tv, calls, _ := tavily(t, c.reply)
		_, err := tv.Search(context.Background(), "q")
		if err == nil || (c.want != "" && err.Error() != c.want) {
			t.Errorf("error %v, want %q", err, c.want)
		}
		if len(*calls) != c.calls {
			t.Errorf("%q: asked %d times, want %d", c.want, len(*calls), c.calls)
		}
	}
}

func TestSearchRetries(t *testing.T) {
	tv, calls, waits := tavily(t, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(429)
	}, status(200, found))
	got, err := tv.Search(context.Background(), "q")
	if err != nil || len(got) != 2 || len(*calls) != 2 {
		t.Fatalf("%d results, %d calls, error %v", len(got), len(*calls), err)
	}
	if fmt.Sprint(*waits) != "[2s]" {
		t.Errorf("waited %v", *waits)
	}

	tv, calls, waits = tavily(t, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	}, status(200, found))
	if _, err := tv.Search(context.Background(), "q"); err != nil || fmt.Sprint(*waits) != "[5s]" {
		t.Errorf("waited %v, error %v", *waits, err)
	}

	tv, calls, _ = tavily(t, status(503, ""), status(200, found))
	if got, err := tv.Search(context.Background(), "q"); err != nil || len(got) != 2 || len(*calls) != 2 {
		t.Errorf("%d results, %d calls, error %v", len(got), len(*calls), err)
	}
}

func TestSearchPlainRequest(t *testing.T) {
	tv, calls, waits := tavily(t, status(400, `{"detail":{"error":"Unknown field include_published_date"}}`), status(200, found))
	got, err := tv.Search(context.Background(), "q")
	if err != nil || len(got) != 2 || len(*calls) != 2 || len(*waits) != 0 {
		t.Fatalf("%d results, %d calls, %v waits, error %v", len(got), len(*calls), *waits, err)
	}
	if b, _ := json.Marshal((*calls)[1].body); string(b) != `{"max_results":5,"query":"q"}` {
		t.Errorf("second body %s", b)
	}

	tv, calls, waits = tavily(t, status(400, `{"detail":{"error":"bad"}}`), status(503, ""))
	if _, err := tv.Search(context.Background(), "q"); err == nil || len(*calls) != 2 || len(*waits) != 0 {
		t.Errorf("%d calls, %v waits, error %v", len(*calls), *waits, err)
	}

	tv, calls, _ = tavily(t, status(422, `{"detail":[{"loc":["body","query"],"msg":"field required"}]}`))
	_, err = tv.Search(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "field required") || len(*calls) != 2 {
		t.Errorf("%d calls, error %v", len(*calls), err)
	}
}

func TestSearchUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	n := 0
	tv := Tavily{Key: "k", SearchURL: url, wait: func(context.Context, time.Duration) error { n++; return nil }}
	_, err := tv.Search(context.Background(), "q")
	if err == nil || !strings.HasPrefix(err.Error(), "连不上 Tavily：") || n != 1 {
		t.Errorf("error %v after %d waits", err, n)
	}
}

func TestSearchCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-release
	}))
	defer srv.Close()
	defer close(release)
	_, err := Tavily{Key: "k", SearchURL: srv.URL}.Search(ctx, "q")
	if err != context.Canceled {
		t.Errorf("error %v", err)
	}
}

func TestTavilyRead(t *testing.T) {
	tv, calls, _ := tavily(t, status(200, `{"results":[{"url":"https://go.dev/blog/go1.27","title":" Go 1.27\n is released ","raw_content":"\n# Go 1.27 is released\n\nToday the Go team…\n","images":[]}],"failed_results":[],"response_time":0.2}`))
	got, err := tv.Read(context.Background(), "https://go.dev/blog/go1.27")
	if err != nil {
		t.Fatal(err)
	}
	want := Page{URL: "https://go.dev/blog/go1.27", Title: "Go 1.27 is released", Text: "# Go 1.27 is released\n\nToday the Go team…"}
	if *got != want {
		t.Errorf("page %+v\nwant %+v", *got, want)
	}
	c := (*calls)[0]
	b, _ := json.Marshal(c.body)
	if c.path != "/extract" || c.auth != "Bearer tvly-test" || string(b) != `{"extract_depth":"basic","format":"markdown","urls":["https://go.dev/blog/go1.27"]}` {
		t.Errorf("path %q, authorization %q, body %s", c.path, c.auth, b)
	}
}

func TestTavilyReadFails(t *testing.T) {
	cases := []struct {
		reply func(w http.ResponseWriter)
		want  string
		calls int
	}{
		{status(200, `{"results":[],"failed_results":[{"url":"https://go.dev/x","error":"404 page not found"}]}`), locales.ReadFailed("404 page not found"), 1},
		{status(200, `{"results":[{"url":"https://go.dev/x","raw_content":"  "}],"failed_results":[]}`), locales.ReadEmpty(), 1},
		{status(200, `{"results":[],"failed_results":[]}`), locales.ReadEmpty(), 1},
		{status(401, `{"detail":{"error":"Unauthorized"}}`), locales.WebBadKey("Tavily"), 1},
		{status(503, ``), locales.WebHTTPError("Tavily", "503", "Service Unavailable"), 2},
	}
	for _, c := range cases {
		tv, calls, _ := tavily(t, c.reply)
		_, err := tv.Read(context.Background(), "https://go.dev/x")
		if err == nil || err.Error() != c.want || len(*calls) != c.calls {
			t.Errorf("error %v after %d calls, want %q after %d", err, len(*calls), c.want, c.calls)
		}
	}
}

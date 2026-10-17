package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Qsnh/Murmur/locales"
)

func deepSeek(t *testing.T, replies ...func(w http.ResponseWriter)) (DeepSeek, *[]call, *[]time.Duration) {
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
	ds := DeepSeek{Key: " sk-deepseek-test ", SearchURL: srv.URL + "/v1/tools/search", FetchURL: srv.URL + "/v1/tools/fetch", wait: func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}}
	return ds, &calls, &waits
}

const sought = `{"results":[
	{"rerank_score":2.4,"title":"DeepSeek V4\n model released","url":"https://example.com/deepseek-v4","snippet":"DeepSeek V4 is now available.","summary":"DeepSeek has released the V4 model, with tool calling and long context.","content":"","site_name":"Example News","site_icon":null,"date":"2026-05-31T16:00:00+00:00","authority":3},
	{"rerank_score":1.2,"title":"Again","url":"https://example.com/deepseek-v4#1","snippet":"dup","summary":"dup","content":"","site_name":null,"site_icon":null,"date":null,"authority":-1},
	{"rerank_score":1.0,"title":"","url":"https://example.org/b?id=2#1","snippet":"Only a snippet.","summary":"","content":"","site_name":null,"site_icon":null,"date":"2026-06-01T01:30:00Z","authority":0},
	{"rerank_score":0.5,"title":"no url","url":"","snippet":"x","summary":"x","content":"","date":null,"authority":0}
]}`

func TestDeepSeekSearch(t *testing.T) {
	ds, calls, _ := deepSeek(t, status(200, sought))
	got, err := ds.Search(context.Background(), "  DeepSeek V4 发布 ")
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{Title: "DeepSeek V4 model released", URL: "https://example.com/deepseek-v4", Content: "DeepSeek has released the V4 model, with tool calling and long context.", Published: "2026-06-01"},
		{Title: "https://example.org/b?id=2", URL: "https://example.org/b?id=2", Content: "Only a snippet.", Published: "2026-06-01T09:30:00+08:00"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("results %+v\nwant %+v", got, want)
	}
	c := (*calls)[0]
	b, _ := json.Marshal(c.body)
	if c.auth != "Bearer sk-deepseek-test" || c.path != "/v1/tools/search" || string(b) != `{"count":5,"query":"DeepSeek V4 发布"}` {
		t.Errorf("call %+v, body %s", c, b)
	}
}

func TestDeepSeekClipsQuery(t *testing.T) {
	ds, calls, _ := deepSeek(t, status(200, `{"results":[]}`))
	got, err := ds.Search(context.Background(), strings.Repeat("搜", 127)+" 索词")
	if err != nil || len(got) != 0 {
		t.Fatalf("results %+v, error %v", got, err)
	}
	if q := (*calls)[0].body["query"]; q != strings.Repeat("搜", 127) {
		t.Errorf("query %q", q)
	}
}

func TestDeepSeekSearchErrors(t *testing.T) {
	cases := []struct {
		reply func(w http.ResponseWriter)
		want  string
		calls int
	}{
		{status(401, `{"error":{"message":"Authentication Fails","type":"authentication_error","param":null,"code":"invalid_request_error"}}`), locales.WebBadKey("DeepSeek"), 1},
		{status(402, `{"error":{"message":"Insufficient Balance","type":"unknown_error","param":null,"code":"invalid_request_error"}}`), locales.WebNoBalance("DeepSeek"), 1},
		{status(400, `{"error":{"message":"'query' must contain 1 to 128 characters","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`), locales.WebHTTPError("DeepSeek", "400", "'query' must contain 1 to 128 characters"), 1},
		{status(429, `{"error":{"message":"Rate limit reached","type":"rate_limit_error","param":null,"code":"invalid_request_error"}}`), locales.WebRateLimited("DeepSeek"), 2},
		{status(504, `{"error":{"message":"upstream timeout","type":"api_error","param":null,"code":"invalid_request_error"}}`), locales.WebHTTPError("DeepSeek", "504", "upstream timeout"), 2},
	}
	for _, c := range cases {
		ds, calls, _ := deepSeek(t, c.reply)
		_, err := ds.Search(context.Background(), "q")
		if err == nil || err.Error() != c.want || len(*calls) != c.calls {
			t.Errorf("error %v after %d calls, want %q after %d", err, len(*calls), c.want, c.calls)
		}
	}
}

func TestDeepSeekRead(t *testing.T) {
	ds, calls, _ := deepSeek(t, status(200, `{"title":" Go 1.27\n is released ","content":"\n# Go 1.27 is released\n\nToday…\n"}`))
	got, err := ds.Read(context.Background(), "https://go.dev/blog/go1.27")
	if err != nil {
		t.Fatal(err)
	}
	want := Page{URL: "https://go.dev/blog/go1.27", Title: "Go 1.27 is released", Text: "# Go 1.27 is released\n\nToday…"}
	if *got != want {
		t.Errorf("page %+v\nwant %+v", *got, want)
	}
	c := (*calls)[0]
	b, _ := json.Marshal(c.body)
	if c.auth != "Bearer sk-deepseek-test" || c.path != "/v1/tools/fetch" || string(b) != `{"url":"https://go.dev/blog/go1.27"}` {
		t.Errorf("call %+v, body %s", c, b)
	}
}

func TestDeepSeekReadUnescapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`\# Go 1\.22 is released\!

Eli Bendersky\, on behalf of the Go team

values \:\= \[\]string\{\"a\"\, \"b\"\}
截至2026\-10\-16，均值 15\.82 元，增长 537\.9\%
年度 \| 均值
\-\-\-\|\-\-\-
C\:\\Go\\bin`, `# Go 1.22 is released!

Eli Bendersky, on behalf of the Go team

values := []string{"a", "b"}
截至2026-10-16，均值 15.82 元，增长 537.9%
年度 | 均值
---|---
C:\Go\bin`},
		{"# Title\n\nUse `a\\.b` to match a dot, and 1\\. at the start of a line.\n\n- one, two.", "# Title\n\nUse `a\\.b` to match a dot, and 1\\. at the start of a line.\n\n- one, two."},
		{"没有标点的中文", "没有标点的中文"},
	}
	for _, c := range cases {
		b, _ := json.Marshal(map[string]string{"title": "t", "content": c.in})
		ds, _, _ := deepSeek(t, status(200, string(b)))
		got, err := ds.Read(context.Background(), "https://go.dev/x")
		if err != nil || got.Text != c.want {
			t.Errorf("text %q, error %v\nwant %q", got.Text, err, c.want)
		}
	}
}

func TestDeepSeekReadFails(t *testing.T) {
	cases := []struct {
		reply func(w http.ResponseWriter)
		want  string
		calls int
	}{
		{status(404, `{"error":{"message":"no usable content","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`), locales.ReadUnavailable(), 1},
		{status(403, `{"error":{"message":"rejected","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`), locales.ReadRefused("DeepSeek"), 1},
		{status(504, `{"error":{"message":"downstream timeout","type":"api_error","param":null,"code":"invalid_request_error"}}`), locales.ReadTimeout(), 1},
		{status(402, `{"error":{"message":"Insufficient Balance","type":"unknown_error","param":null,"code":"invalid_request_error"}}`), locales.WebNoBalance("DeepSeek"), 1},
		{status(200, `{"title":"x","content":"  "}`), locales.ReadEmpty(), 1},
		{status(500, `{"error":{"message":"boom","type":"api_error","param":null,"code":"invalid_request_error"}}`), locales.WebHTTPError("DeepSeek", "500", "boom"), 2},
	}
	for _, c := range cases {
		ds, calls, _ := deepSeek(t, c.reply)
		_, err := ds.Read(context.Background(), "https://go.dev/x")
		if err == nil || err.Error() != c.want || len(*calls) != c.calls {
			t.Errorf("error %v after %d calls, want %q after %d", err, len(*calls), c.want, c.calls)
		}
	}
}

func TestDeepSeekSearchNotFoundIsNotARead(t *testing.T) {
	ds, _, _ := deepSeek(t, status(404, `{"error":{"message":"Not Found","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`))
	_, err := ds.Search(context.Background(), "q")
	if err == nil || err.Error() != locales.WebHTTPError("DeepSeek", "404", "Not Found") {
		t.Errorf("error %v", err)
	}
}

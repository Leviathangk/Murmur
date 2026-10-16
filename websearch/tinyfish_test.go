package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Qsnh/Murmur/locales"
)

type fishCall struct {
	key    string
	method string
	path   string
	query  string
	body   map[string]any
}

func tinyFish(t *testing.T, replies ...func(w http.ResponseWriter)) (TinyFish, *[]fishCall, *[]time.Duration) {
	t.Helper()
	var calls []fishCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, fishCall{r.Header.Get("X-API-Key"), r.Method, r.URL.Path, r.URL.RawQuery, body})
		replies[min(len(calls), len(replies))-1](w)
	}))
	t.Cleanup(srv.Close)
	var waits []time.Duration
	tf := TinyFish{Key: " sk-tinyfish-test ", SearchURL: srv.URL + "/search", FetchURL: srv.URL + "/fetch", wait: func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}}
	return tf, &calls, &waits
}

const fished = `{"query":"Go 1.27 发布","results":[
	{"date":"Aug 14, 2026","position":1,"site_name":"go.dev","snippet":"Go 1.27 adds…","title":"Go 1.27\n is released","url":"https://go.dev/blog/go1.27"},
	{"position":2,"site_name":"go.dev","snippet":"dup","title":"Go again","url":"https://go.dev/blog/go1.27"},
	{"position":3,"site_name":"example.com","snippet":"untitled","title":"","url":"https://example.com/a"},
	{"position":4,"snippet":"no url","title":"x","url":""}
],"total_results":4,"page":0}`

func TestTinyFishSearch(t *testing.T) {
	tf, calls, _ := tinyFish(t, status(200, fished))
	got, err := tf.Search(context.Background(), "  Go 1.27 发布 ")
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{Title: "Go 1.27 is released", URL: "https://go.dev/blog/go1.27", Content: "Go 1.27 adds…", Published: "Aug 14, 2026"},
		{Title: "https://example.com/a", URL: "https://example.com/a", Content: "untitled"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("results %+v\nwant %+v", got, want)
	}
	c := (*calls)[0]
	if c.key != "sk-tinyfish-test" || c.method != "GET" || c.path != "/search" || c.query != "query=Go+1.27+%E5%8F%91%E5%B8%83" {
		t.Errorf("call %+v", c)
	}
}

func TestTinyFishSearchErrors(t *testing.T) {
	cases := []struct {
		reply func(w http.ResponseWriter)
		want  string
		calls int
	}{
		{status(401, `{"error":{"code":"INVALID_API_KEY","message":"Invalid or expired API key"}}`), locales.WebBadKey("TinyFish"), 1},
		{status(402, `{"error":{"code":"INSUFFICIENT_CREDITS","message":"Daily free allowance used"}}`), locales.WebDailyQuota("TinyFish"), 1},
		{status(400, `{"error":{"code":"INVALID_INPUT","message":"Field \"query\": Invalid input"}}`), locales.WebHTTPError("TinyFish", "400", `Field "query": Invalid input`), 1},
		{status(503, `{"error":{"code":"UNAVAILABLE","message":"try later"}}`), locales.WebHTTPError("TinyFish", "503", "try later"), 2},
		{status(429, `{"error":{"code":"RATE_LIMITED","message":"slow down"}}`), locales.WebRateLimited("TinyFish"), 2},
	}
	for _, c := range cases {
		tf, calls, _ := tinyFish(t, c.reply)
		_, err := tf.Search(context.Background(), "q")
		if err == nil || err.Error() != c.want || len(*calls) != c.calls {
			t.Errorf("error %v after %d calls, want %q after %d", err, len(*calls), c.want, c.calls)
		}
	}

	tf, calls, waits := tinyFish(t, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(429)
	}, status(200, fished))
	if got, err := tf.Search(context.Background(), "q"); err != nil || len(got) != 2 || len(*calls) != 2 || fmt.Sprint(*waits) != "[3s]" {
		t.Errorf("%d results after %d calls and waits %v, error %v", len(got), len(*calls), *waits, err)
	}
}

func TestTinyFishRead(t *testing.T) {
	tf, calls, _ := tinyFish(t, status(200, `{"results":[{"url":"https://go.dev/blog/go1.27","final_url":"https://go.dev/blog/go1.27","title":"Go 1.27 is released - The Go Programming Language","description":null,"language":"en","text":"# Go 1.27 is released\n\nToday…\n","author":null,"published_date":"2026-08-12T00:00:00+08:00","latency_ms":246,"format":"markdown"}],"errors":[]}`))
	got, err := tf.Read(context.Background(), "https://go.dev/blog/go1.27")
	if err != nil {
		t.Fatal(err)
	}
	want := Page{URL: "https://go.dev/blog/go1.27", Title: "Go 1.27 is released - The Go Programming Language", Text: "# Go 1.27 is released\n\nToday…", Published: "2026-08-12T00:00:00+08:00"}
	if *got != want {
		t.Errorf("page %+v\nwant %+v", *got, want)
	}
	c := (*calls)[0]
	b, _ := json.Marshal(c.body)
	if c.key != "sk-tinyfish-test" || c.method != "POST" || c.path != "/fetch" || string(b) != `{"format":"markdown","per_url_timeout_ms":45000,"urls":["https://go.dev/blog/go1.27"]}` {
		t.Errorf("call %+v, body %s", c, b)
	}
}

func TestTinyFishReadFails(t *testing.T) {
	cases := []struct {
		reply func(w http.ResponseWriter)
		want  string
		calls int
	}{
		{status(200, `{"results":[],"errors":[{"url":"https://go.dev/x","error":"page_not_found","status":404}]}`), locales.ReadNotFound(), 1},
		{status(200, `{"results":[],"errors":[{"url":"https://go.dev/x","error":"target_http_error","status":403}]}`), locales.ReadHTTPError("403"), 1},
		{status(200, `{"results":[],"errors":[{"url":"https://go.dev/x","error":"bot_blocked"}]}`), locales.ReadBlocked(), 1},
		{status(200, `{"results":[],"errors":[{"url":"ftp://go.dev","error":"invalid_url"}]}`), locales.ReadInvalidURL(), 1},
		{status(200, `{"results":[],"errors":[{"url":"https://go.dev/x","error":"something_new"}]}`), locales.ReadFailed("something_new"), 1},
		{status(200, `{"results":[{"url":"https://go.dev/x","title":"x","text":null}],"errors":[]}`), locales.ReadEmpty(), 1},
		{status(402, `{"error":{"code":"FREE_ALLOWANCE_EXHAUSTED","message":"used"}}`), locales.WebDailyQuota("TinyFish"), 1},
		{status(500, `{"error":{"code":"INTERNAL","message":"boom"}}`), locales.WebHTTPError("TinyFish", "500", "boom"), 2},
	}
	for _, c := range cases {
		tf, calls, _ := tinyFish(t, c.reply)
		_, err := tf.Read(context.Background(), "https://go.dev/x")
		if err == nil || err.Error() != c.want || len(*calls) != c.calls {
			t.Errorf("error %v after %d calls, want %q after %d", err, len(*calls), c.want, c.calls)
		}
	}
}

func TestReadDoesNotRetryTimeouts(t *testing.T) {
	release := make(chan struct{})
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		<-release
	}))
	defer srv.Close()
	defer close(release)
	defer func(d time.Duration) { readTimeout = d }(readTimeout)
	readTimeout = 50 * time.Millisecond
	waited := false
	tf := TinyFish{Key: "k", FetchURL: srv.URL, wait: func(context.Context, time.Duration) error { waited = true; return nil }}
	_, err := tf.Read(context.Background(), "https://go.dev/")
	if err == nil || err.Error() != locales.WebTimeout("TinyFish") || n.Load() != 1 || waited {
		t.Errorf("error %v after %d calls, waited %v", err, n.Load(), waited)
	}
}

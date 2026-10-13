package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"
)

// testEngine points the SDK at an httptest server; mutating calls are only ever made against it.
func testEngine(t *testing.T, h http.HandlerFunc) *sdkEngine {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cli, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), client.WithAPIVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	return &sdkEngine{cli}
}

func frame(stream byte, s string) []byte {
	b := make([]byte, 8, 8+len(s))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:], uint32(len(s)))
	return append(b, s...)
}

func TestActionsUsePost(t *testing.T) {
	var mu sync.Mutex
	var got []string
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	for _, v := range []string{"start", "stop", "restart"} {
		if err := e.Action(context.Background(), "abc", v); err != nil {
			t.Fatal(err)
		}
	}
	want := "POST /v1.47/containers/abc/start,POST /v1.47/containers/abc/stop,POST /v1.47/containers/abc/restart"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v", got)
	}
	if e.Action(context.Background(), "abc", "kill") == nil {
		t.Fatal("kill must be rejected")
	}
}

func TestLogsMultiplexed(t *testing.T) {
	var body bytes.Buffer
	body.Write(frame(1, "t1 out a\nt1 out b"))
	body.Write(frame(2, "t2 err\n"))
	body.Write(frame(1, "\n"))
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			w.Write([]byte(`{"Config":{"Tty":false}}`))
			return
		}
		if q := r.URL.Query(); q.Get("tail") != "5" || q.Get("follow") != "1" || q.Get("timestamps") != "1" {
			t.Errorf("query %v", q)
		}
		w.Write(body.Bytes())
	})
	var got []string
	err := e.Logs(context.Background(), "abc", 5, true, func(s byte, l string) { got = append(got, string(rune('0'+s))+l) })
	if err != nil {
		t.Fatal(err)
	}
	if want := "1t1 out a,2t2 err,1t1 out b"; strings.Join(got, ",") != want {
		t.Fatalf("got %q", got)
	}
}

func TestLogsRawTTY(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			w.Write([]byte(`{"Config":{"Tty":true}}`))
			return
		}
		w.Write([]byte("hello world\r\nsecond line\n"))
	})
	var got []string
	if err := e.Logs(context.Background(), "abc", 10, false, func(_ byte, l string) { got = append(got, l) }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "hello world|second line" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamStatsFrames(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stream") != "true" {
			t.Errorf("query %v", r.URL.Query())
		}
		w.Write([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000,"online_cpus":4},"precpu_stats":{},"memory_stats":{"usage":1000,"limit":5000}}` + "\n"))
		w.Write([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":300},"system_cpu_usage":2000,"online_cpus":4},
"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000},
"memory_stats":{"usage":1000,"limit":5000,"stats":{"inactive_file":200}}}` + "\n"))
	})
	var got []Stats
	if err := e.StreamStats(context.Background(), "abc", func(s Stats) { got = append(got, s) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || cpuPercent(got[0]) != 0 || cpuPercent(got[1]) != 80 || memUsed(got[1]) != 800 {
		t.Fatalf("frames %+v", got)
	}
}

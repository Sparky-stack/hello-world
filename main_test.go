package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg := configFromEnv(func(string) string { return "" })
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.Greeting != "hello" {
		t.Errorf("Greeting = %q, want hello", cfg.Greeting)
	}
	if cfg.Version != version {
		t.Errorf("Version = %q, want %q", cfg.Version, version)
	}
}

func TestConfigFromEnvOverrides(t *testing.T) {
	env := map[string]string{"PORT": "9090", "GREETING": "tere"}
	cfg := configFromEnv(func(k string) string { return env[k] })
	if cfg.Port != "9090" || cfg.Greeting != "tere" {
		t.Errorf("got %+v", cfg)
	}
}

func TestRoot(t *testing.T) {
	h := newHandler(Config{Greeting: "tere", Hostname: "pod-1", Version: "abc1234"}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got Greeting
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := Greeting{Message: "tere", Hostname: "pod-1", Version: "abc1234"}
	if got != want {
		t.Errorf("body = %+v, want %+v", got, want)
	}
}

func TestHealthz(t *testing.T) {
	h := newHandler(Config{}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "ok" {
		t.Errorf("status field = %q", got["status"])
	}
}

func TestVersion(t *testing.T) {
	h := newHandler(Config{Version: "abc1234"}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != "abc1234" {
		t.Errorf("version field = %q, want abc1234", got["version"])
	}
}

func TestRouting(t *testing.T) {
	h := newHandler(Config{}, nil)
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/nope", http.StatusNotFound},
		{http.MethodPost, "/", http.StatusMethodNotAllowed},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodPost, "/version", http.StatusMethodNotAllowed},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

func TestServeGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	// A slow handler proves in-flight requests complete during shutdown.
	started := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, slow, slog.New(slog.DiscardHandler)) }()

	respCh := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			respCh <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		respCh <- string(b)
	}()

	<-started
	cancel()

	if got := <-respCh; got != "done" {
		t.Errorf("in-flight response = %q, want done", got)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after shutdown")
	}
}

func TestConfigFromEnvDatabase(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, false},
		{map[string]string{"DATABASE_URL": "postgresql://u:p@db:5432/orders"}, true},
		{map[string]string{"PGHOST": "db"}, true},
	} {
		if got := configFromEnv(func(k string) string { return tc.env[k] }).Database; got != tc.want {
			t.Errorf("env %v: Database = %v, want %v", tc.env, got, tc.want)
		}
	}
}

func getDB(t *testing.T, cfg Config, ping func(context.Context) error) (int, map[string]string) {
	t.Helper()
	rec := httptest.NewRecorder()
	newHandler(cfg, ping).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/db", nil))
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return rec.Code, body
}

func TestDBNotConfigured(t *testing.T) {
	code, body := getDB(t, Config{}, func(context.Context) error {
		t.Fatal("ping called without a database")
		return nil
	})
	if code != http.StatusServiceUnavailable || body["status"] != "not configured" {
		t.Errorf("got %d %v, want 503 not configured", code, body)
	}
}

func TestDBOK(t *testing.T) {
	code, body := getDB(t, Config{Database: true}, func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("ping context has no deadline")
		}
		return nil
	})
	if code != http.StatusOK || body["status"] != "ok" {
		t.Errorf("got %d %v, want 200 ok", code, body)
	}
}

func TestDBError(t *testing.T) {
	code, body := getDB(t, Config{Database: true}, func(context.Context) error { return errors.New("connection refused") })
	if code != http.StatusServiceUnavailable || body["status"] != "error" || body["error"] != "" {
		t.Errorf("got %d %v, want 503 error", code, body)
	}
}

// pingDB against a port nobody listens on: the real driver path returns an error instead of hanging or panicking.
func TestPingDBUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	t.Setenv("DATABASE_URL", "postgresql://u:p@"+addr+"/orders?sslmode=disable")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pingDB(ctx); err == nil {
		t.Fatal("pingDB succeeded without a database")
	}
}

package source

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A backend that completes the connection but never answers (the observed DGT
// overload mode) must be abandoned at the parent deadline, not blocked on for a
// full 45s attempt — and fetch must surface the deadline error promptly.
func TestFetchHungBackendBoundedByBudget(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // hang until the client gives up
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _, err := fetch(ctx, Options{}, srv.URL)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error against a hung backend, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("fetch blocked %v on a hung backend; the parent budget should bound it", elapsed)
	}
}

// The core fix: a per-attempt timeout drops a hung attempt so a retry can land
// on a now-healthy worker. First request hangs past perAttemptTimeout; the
// retry succeeds.
func TestFetchRetryAfterHungAttempt(t *testing.T) {
	restore := perAttemptTimeout
	perAttemptTimeout = 150 * time.Millisecond
	defer func() { perAttemptTimeout = restore }()

	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			<-r.Context().Done() // first attempt: hang until it is abandoned
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, _, fromCache, err := fetch(ctx, Options{}, srv.URL)
	if err != nil {
		t.Fatalf("expected success after retrying past a hung attempt, got %v", err)
	}
	if fromCache {
		t.Fatal("no cache configured; result must be a live fetch")
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected body %q", body)
	}
}

// A transient 5xx is retried and the eventual 200 is returned.
func TestFetchRetriesTransient5xx(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, _, _, err := fetch(ctx, Options{}, srv.URL)
	if err != nil {
		t.Fatalf("expected success after transient 5xx, got %v", err)
	}
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Fatalf("expected 2 attempts (503, 200), got %d", got)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestFetchHonoursMaxAttempts(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, _, _, err := fetch(context.Background(), Options{MaxAttempts: 1}, srv.URL)
	if err == nil {
		t.Fatal("expected an error on 503")
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("expected exactly 1 interactive attempt, got %d", got)
	}
}

// A definitive 4xx is not retried — it is not a transient condition.
func TestFetchDoesNotRetry4xx(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, _, err := fetch(ctx, Options{}, srv.URL)
	if err == nil {
		t.Fatal("expected an error on 400")
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("expected exactly 1 attempt for a 4xx, got %d", got)
	}
}

package httputil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// counterServer counts attempts and returns the configured status until
// `succeedAt` calls in, then returns 200.
func counterServer(t *testing.T, failingStatus int, succeedAt int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if int(n) >= succeedAt {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(failingStatus)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestDoWithRetryRecoversFromTransient503(t *testing.T) {
	srv, calls := counterServer(t, http.StatusServiceUnavailable, 2)

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoWithRetry(&http.Client{Timeout: 2 * time.Second}, req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("got %d calls, want 2 (1 fail + 1 success)", got)
	}
}

func TestDoWithRetryGivesUpAfterMax(t *testing.T) {
	srv, calls := counterServer(t, http.StatusBadGateway, 99)

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoWithRetry(&http.Client{Timeout: 2 * time.Second}, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected last 502 response, got %d", resp.StatusCode)
	}
	if got := calls.Load(); got != int32(MaxAttempts) {
		t.Errorf("got %d calls, want %d", got, MaxAttempts)
	}
}

func TestDoWithRetrySkipsNonIdempotent(t *testing.T) {
	srv, calls := counterServer(t, http.StatusServiceUnavailable, 99)

	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	_, _ = DoWithRetry(&http.Client{Timeout: 2 * time.Second}, req)
	if got := calls.Load(); got != 1 {
		t.Errorf("POST should not retry, got %d calls", got)
	}
}

func TestDoWithRetryAcceptsNon429NonGatewayStatus(t *testing.T) {
	// 404 is not retryable.
	srv, calls := counterServer(t, http.StatusNotFound, 99)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoWithRetry(&http.Client{Timeout: 2 * time.Second}, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("got %d, want 404", resp.StatusCode)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("404 should not retry, got %d calls", got)
	}
}

// Sanity-check the helper functions in isolation too.
func TestRetryableStatusAndIdempotent(t *testing.T) {
	for _, code := range []int{429, 502, 503, 504} {
		if !retryableStatus(code) {
			t.Errorf("status %d should be retryable", code)
		}
	}
	for _, code := range []int{200, 400, 401, 403, 404, 500} {
		if retryableStatus(code) {
			t.Errorf("status %d should NOT be retryable", code)
		}
	}
	for _, m := range []string{"GET", "HEAD", "PUT", "DELETE", "OPTIONS"} {
		if !isIdempotent(m) {
			t.Errorf("method %s should be idempotent", m)
		}
	}
	for _, m := range []string{"POST", "PATCH"} {
		if isIdempotent(m) {
			t.Errorf("method %s should NOT be idempotent", m)
		}
	}
}

// Smoke test that we don't blow up on connection errors (server closed).
func TestDoWithRetryPropagatesConnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Close() // intentionally closed

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	if _, err := DoWithRetry(&http.Client{Timeout: 500 * time.Millisecond}, req); err == nil {
		t.Error("expected connection error after retries exhausted")
	}
}

// Document the example of using DoWithRetry — not a real test, just a
// smoke that the package compiles when used as expected.
func ExampleDoWithRetry() {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	resp, err := DoWithRetry(http.DefaultClient, req)
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	defer resp.Body.Close()
}

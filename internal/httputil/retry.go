// Package httputil bundles cross-cutting HTTP helpers used by both the
// apim and deepflow clients. Today: a method-aware retry wrapper.
package httputil

import (
	"errors"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// MaxAttempts is the upper bound on tries (initial + retries) per spec
// claude/specs/operations_guide.md §6.
const MaxAttempts = 3

// initialBackoff is the first delay before retrying. Doubles each attempt.
const initialBackoff = 100 * time.Millisecond

// retryableStatus reports whether an HTTP status code is worth retrying.
// Per spec §6: 429 (rate limited), 502/503/504 (transient gateway/upstream).
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// isIdempotent reports whether the HTTP method is safe to retry. Per spec
// §6: GET/HEAD/PUT/DELETE/OPTIONS only. POST and PATCH are NEVER retried —
// retrying could cause duplicate writes.
func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// DoWithRetry runs req through client with the spec's retry policy:
//   - non-idempotent methods (POST, PATCH): no retry, single attempt
//   - idempotent methods: up to MaxAttempts on transport error or
//     retryable status, with exponential backoff + jitter
//
// On retry, the response body from the failed attempt is drained and
// closed so connection pools stay healthy. The req.Body must be re-readable
// across attempts — pass http.NoBody (or an in-memory reader) for write
// methods if you somehow need POST retry.
func DoWithRetry(client *http.Client, req *http.Request) (*http.Response, error) {
	if !isIdempotent(req.Method) {
		return client.Do(req)
	}

	var (
		resp    *http.Response
		err     error
		backoff = initialBackoff
	)
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		resp, err = client.Do(req)
		if err == nil && !retryableStatus(resp.StatusCode) {
			return resp, nil
		}
		if attempt < MaxAttempts {
			// Drain + close so the pooled connection is reusable.
			if resp != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			// Sleep with jitter — full-jitter half-spread is plenty here.
			jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
			time.Sleep(backoff + jitter)
			backoff *= 2
		}
	}
	if err == nil {
		// Last attempt was a retryable status code; return the response so
		// the caller can inspect the body.
		return resp, nil
	}
	return nil, errors.Join(err, errors.New("retry exhausted"))
}

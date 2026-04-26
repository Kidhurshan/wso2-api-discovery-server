package health

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

// startTestServer launches the health server on an ephemeral port and returns
// the chosen URL plus a cancel function. The shared listener trick is needed
// because Server.Run uses srv.ListenAndServe(), which picks the addr from
// the Server struct — so we pre-pick a free port via net.Listen, close it,
// and pass its address through.
func startTestServer(t *testing.T, state State) (string, context.CancelFunc) {
	t.Helper()

	addr := pickFreePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = New(addr, state, zaptest.NewLogger(t)).Run(ctx)
	}()

	// Poll for server readiness — a tiny race exists between Run() starting
	// the goroutine and the listener actually accepting.
	url := "http://" + addr
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url + "/healthz"); err == nil {
			resp.Body.Close()
			return url, cancel
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatal("health server did not come up within 2s")
	return "", nil
}

func TestLivenessAlwaysOK(t *testing.T) {
	url, stop := startTestServer(t, NewStaticState(false))
	defer stop()

	resp, err := http.Get(url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("liveness status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Errorf("body: %s", body)
	}
}

func TestReadinessReflectsDB(t *testing.T) {
	state := NewStaticState(true)
	url, stop := startTestServer(t, state)
	defer stop()

	// Healthy
	resp, err := http.Get(url + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("ready: expected 200, got %d", resp.StatusCode)
	}
	var ready readinessReport
	_ = json.NewDecoder(resp.Body).Decode(&ready)
	resp.Body.Close()
	if !ready.DatabaseReachable || ready.Status != "ready" {
		t.Errorf("ready report: %+v", ready)
	}

	// Flip to unhealthy
	state.SetDBReachable(false)
	resp, _ = http.Get(url + "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("not_ready: expected 503, got %d", resp.StatusCode)
	}
	var notReady readinessReport
	_ = json.NewDecoder(resp.Body).Decode(&notReady)
	resp.Body.Close()
	if notReady.DatabaseReachable || notReady.Status != "not_ready" {
		t.Errorf("not_ready report: %+v", notReady)
	}
}

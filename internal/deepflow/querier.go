package deepflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wso2/api-discovery-server/internal/config"
)

// querierClient implements Client against the DeepFlow querier HTTP API
// (typically http://<deepflow_host>:30617/v1/query/).
type querierClient struct {
	baseURL string
	http    *http.Client
}

// New returns a Client wired to the querier endpoint described by cfg.
//
// cfg.ClickHouseURL is reused as the querier base URL — the field name is
// historical; in practice it must point at deepflow-server's NodePort, not
// the underlying ClickHouse pod.
func New(cfg *config.DeepFlowConfig) (Client, error) {
	if cfg == nil || cfg.ClickHouseURL == "" {
		return nil, fmt.Errorf("deepflow: clickhouse_url is required")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &querierClient{
		baseURL: strings.TrimRight(cfg.ClickHouseURL, "/"),
		http: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// Query POSTs sql to /v1/query/ and parses the column-oriented response into
// row-oriented maps.
func (c *querierClient) Query(ctx context.Context, db, sql string) ([]Row, error) {
	payload, err := json.Marshal(map[string]string{"db": db, "sql": sql})
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/query/", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute query: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("querier returned HTTP %d: %s", resp.StatusCode, snippet(body))
	}

	var envelope querierResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w (body: %s)", err, snippet(body))
	}

	if envelope.OptStatus != "" && envelope.OptStatus != "SUCCESS" {
		return nil, fmt.Errorf("querier error %s: %s", envelope.OptStatus, envelope.Description)
	}

	if len(envelope.Result.Columns) == 0 {
		return nil, nil
	}

	rows := make([]Row, 0, len(envelope.Result.Values))
	for _, vals := range envelope.Result.Values {
		row := make(Row, len(envelope.Result.Columns))
		for i, col := range envelope.Result.Columns {
			if i < len(vals) {
				row[col] = vals[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Ping uses SHOW TABLES, which the querier passes through directly. SELECT 1
// gets rewritten to "SELECT 1 FROM dual" and fails — see project memory
// "deepflow_querier_strategy" for context.
func (c *querierClient) Ping(ctx context.Context) error {
	_, err := c.Query(ctx, "flow_log", "SHOW TABLES")
	return err
}

// Close is a no-op; net/http connections are pooled by the transport.
func (c *querierClient) Close() {}

// querierResponse is the column-oriented envelope DeepFlow returns.
type querierResponse struct {
	OptStatus   string `json:"OPT_STATUS"`
	Description string `json:"DESCRIPTION"`
	Result      struct {
		Columns []string `json:"columns"`
		Values  [][]any  `json:"values"`
	} `json:"result"`
}

// snippet returns at most 200 bytes of body for inclusion in error messages.
func snippet(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + "...(truncated)"
	}
	return string(b)
}

package apim

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/config"
	"github.com/wso2/api-discovery-server/internal/httputil"
)

// APISummary is the per-API entry in the /apis list endpoint. Only fields
// the resolver/expander need are decoded.
type APISummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Context         string `json:"context"`
	LifeCycleStatus string `json:"lifeCycleStatus"`
	UpdatedTime     string `json:"updatedTime"`
}

// APIDetail is the full /apis/{id} response. We keep only the operations,
// endpointConfig, and identifying fields.
type APIDetail struct {
	APISummary
	Provider       string         `json:"provider"`
	EndpointConfig EndpointConfig `json:"endpointConfig"`
	Operations     []Operation    `json:"operations"`
}

// EndpointConfig holds the production_endpoints sub-tree. WSO2's payload
// has more fields (production_endpoints array, sandbox, etc.); we only
// pull what the resolver needs.
type EndpointConfig struct {
	EndpointType        string              `json:"endpoint_type"`
	ProductionEndpoints *ProductionEndpoint `json:"production_endpoints"`
}

// ProductionEndpoint can be either a single object {"url":"..."} or an
// array of them in load-balanced configurations. v1 handles only the
// single form; the array form is logged as a warning by the resolver.
type ProductionEndpoint struct {
	URL string `json:"url"`
}

// Operation describes one (verb, target) pair on an API.
type Operation struct {
	Verb             string `json:"verb"`
	Target           string `json:"target"`
	AuthType         string `json:"authType"`
	ThrottlingPolicy string `json:"throttlingPolicy"`
}

// Pagination matches the WSO2 envelope.
type Pagination struct {
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	Total    int    `json:"total"`
	Next     string `json:"next"`
	Previous string `json:"previous"`
}

// listResponse wraps the paginated /apis response.
type listResponse struct {
	Count      int          `json:"count"`
	List       []APISummary `json:"list"`
	Pagination Pagination   `json:"pagination"`
}

// PublisherClient is the high-level Publisher REST surface. Construct via
// NewPublisherClient; reuse a single instance across the daemon.
type PublisherClient struct {
	baseURL          string
	auth             *Auth
	http             *http.Client
	fetchConcurrency int
	log              *zap.Logger
}

// NewPublisherClient wires the dependencies. fetchConcurrency caps how many
// detail fetches run in parallel.
func NewPublisherClient(cfg *config.APIMConfig, auth *Auth, fetchConcurrency int, log *zap.Logger) *PublisherClient {
	tr := &http.Transport{}
	if !cfg.VerifySSL {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if fetchConcurrency <= 0 {
		fetchConcurrency = 5
	}
	return &PublisherClient{
		baseURL:          cfg.PublisherURL,
		auth:             auth,
		http:             &http.Client{Transport: tr, Timeout: timeout},
		fetchConcurrency: fetchConcurrency,
		log:              log,
	}
}

// ListPublishedAPIs walks /api/am/publisher/v4/apis with pagination and
// returns only APIs in PUBLISHED lifecycle. Per spec §4.1 we filter
// client-side because server-side filtering is unreliable across versions.
func (c *PublisherClient) ListPublishedAPIs(ctx context.Context) ([]APISummary, error) {
	const pageSize = 200
	var all []APISummary
	offset := 0

	for {
		page, err := c.listOnce(ctx, offset, pageSize)
		if err != nil {
			return nil, fmt.Errorf("list offset %d: %w", offset, err)
		}
		for _, a := range page.List {
			if a.LifeCycleStatus == "PUBLISHED" {
				all = append(all, a)
			}
		}
		if page.Pagination.Next == "" {
			break
		}
		offset += pageSize
		if offset >= page.Pagination.Total {
			break
		}
	}
	return all, nil
}

func (c *PublisherClient) listOnce(ctx context.Context, offset, limit int) (*listResponse, error) {
	url := fmt.Sprintf("%s/api/am/publisher/v4/apis?limit=%d&offset=%d", c.baseURL, limit, offset)
	body, err := c.doGET(ctx, url)
	if err != nil {
		return nil, err
	}
	var lr listResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("decode list page: %w (body: %s)", err, snippet(body))
	}
	return &lr, nil
}

// FetchDetails pulls /apis/{id} for each id with bounded concurrency.
// Errors are accumulated; partial results plus an aggregated error are
// returned so the caller can decide whether to proceed (recommended).
func (c *PublisherClient) FetchDetails(ctx context.Context, ids []string) ([]APIDetail, error) {
	sem := make(chan struct{}, c.fetchConcurrency)
	var (
		mu      sync.Mutex
		results = make([]APIDetail, 0, len(ids))
		errs    []error
	)

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()

			d, err := c.fetchDetail(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("fetch %s: %w", id, err))
				return
			}
			results = append(results, d)
		}(id)
	}
	wg.Wait()

	if len(errs) > 0 {
		return results, errors.Join(errs...)
	}
	return results, nil
}

func (c *PublisherClient) fetchDetail(ctx context.Context, id string) (APIDetail, error) {
	url := fmt.Sprintf("%s/api/am/publisher/v4/apis/%s", c.baseURL, id)
	body, err := c.doGET(ctx, url)
	if err != nil {
		return APIDetail{}, err
	}
	var d APIDetail
	if err := json.Unmarshal(body, &d); err != nil {
		return APIDetail{}, fmt.Errorf("decode detail: %w (body: %s)", err, snippet(body))
	}
	return d, nil
}

// doGET attaches a fresh bearer token and reads the body. Caller decodes.
func (c *PublisherClient) doGET(ctx context.Context, url string) ([]byte, error) {
	tok, err := c.auth.GetToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")

	// httputil.DoWithRetry handles transient 429/502/503/504 + transport
	// errors with exponential backoff. GET is idempotent so this is safe.
	resp, err := httputil.DoWithRetry(c.http, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	return body, nil
}

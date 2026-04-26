package apim

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wso2/api-discovery-server/internal/config"
)

// TokenInfo is the subset of the WSO2 /oauth2/introspect response we use.
// Per RFC 7662, "active" is the only required field; the rest is best-effort.
type TokenInfo struct {
	Active   bool   `json:"active"`
	Scope    string `json:"scope"`
	Username string `json:"username"`
	ClientID string `json:"client_id"`
	Exp      int64  `json:"exp"`
	Iat      int64  `json:"iat"`
}

// Scopes splits Scope on whitespace into the individual scope strings.
// WSO2 returns scopes space-delimited per the OAuth2 spec.
func (t TokenInfo) Scopes() []string {
	if t.Scope == "" {
		return nil
	}
	return strings.Fields(t.Scope)
}

// Introspector calls APIM's /oauth2/introspect to validate bearer tokens.
// Construct via NewIntrospector and reuse — the http.Client pools connections.
type Introspector struct {
	url       string
	basicAuth string
	http      *http.Client
}

// NewIntrospector wires the dependencies. cfg.IntrospectURL and
// cfg.IntrospectBasicAuth must be set; otherwise calls will fail at runtime.
func NewIntrospector(cfg *config.APIMConfig) *Introspector {
	tr := &http.Transport{}
	if !cfg.VerifySSL {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Introspector{
		url:       cfg.IntrospectURL,
		basicAuth: cfg.IntrospectBasicAuth,
		http:      &http.Client{Transport: tr, Timeout: timeout},
	}
}

// Introspect calls /oauth2/introspect with the given token. Returns the
// decoded TokenInfo. Caller must check Active and the desired scope before
// trusting the token — this method does not enforce policy.
//
// Per claude/specs/phase4_admin_portal.md §7.2: POST application/x-www-form-
// urlencoded with body "token=<token>". Authorization: Basic <base64
// client_id:client_secret> (already wrapped in cfg.IntrospectBasicAuth).
func (i *Introspector) Introspect(ctx context.Context, token string) (*TokenInfo, error) {
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.url, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if i.basicAuth != "" {
		req.Header.Set("Authorization", "Basic "+i.basicAuth)
	}

	resp, err := i.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("introspect request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("introspect returned HTTP %d: %s", resp.StatusCode, snippet(body))
	}

	var info TokenInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("introspect decode: %w (body: %s)", err, snippet(body))
	}
	return &info, nil
}

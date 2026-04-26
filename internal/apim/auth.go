// Package apim talks to the WSO2 APIM REST surface: client registration
// (DCR), OAuth2 token issuance and refresh, the Publisher REST API, and
// token introspection.
//
// Clients live in this package so Phase 2 (managed sync) and Phase 4 (the
// BFF in Round 5) can share a single auth path.
package apim

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/config"
)

// Credentials are the DCR-issued client_id/client_secret. Persisted to a
// local file so the daemon doesn't re-register on every restart.
type Credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// Token is the OAuth2 token bundle from /oauth2/token plus our derived
// expiry. Populated on first issuance and refreshed in the background.
type Token struct {
	AccessToken  string
	RefreshToken string
	Scope        string
	TokenType    string
	ExpiresAt    time.Time
}

// dcrRequest is the JSON body for /client-registration/v0.17/register per
// claude/specs/phase2_managed_sync.md §3.1. Fields match WSO2's expected
// payload exactly.
type dcrRequest struct {
	CallbackURL string `json:"callbackUrl"`
	ClientName  string `json:"clientName"`
	Owner       string `json:"owner"`
	GrantType   string `json:"grantType"`
	SaaSApp     bool   `json:"saasApp"`
}

// dcrResponse is the subset of fields we care about from the DCR response.
// WSO2 returns more (jsonString, etc.) but we only need clientId/clientSecret.
type dcrResponse struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// tokenResponse mirrors /oauth2/token's JSON envelope.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// Auth bundles DCR + token issuance + background refresh. Build one with
// NewAuth and call Start before using GetToken.
//
// The struct is safe for concurrent use: GetToken takes the mutex briefly,
// the refresh goroutine swaps the cached token under the same mutex.
type Auth struct {
	cfg    *config.APIMConfig
	log    *zap.Logger
	http   *http.Client
	creds  *Credentials
	credsP string // path where Credentials are persisted

	mu    sync.RWMutex
	token *Token
}

// NewAuth wires the dependencies. credsPath is the file where DCR creds are
// persisted (e.g., /etc/ads/dcr_creds.json). Pass empty to disable persistence
// — useful for tests.
func NewAuth(cfg *config.APIMConfig, log *zap.Logger, credsPath string) *Auth {
	tr := &http.Transport{}
	if !cfg.VerifySSL {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Auth{
		cfg:    cfg,
		log:    log,
		http:   &http.Client{Transport: tr, Timeout: timeout},
		credsP: credsPath,
	}
}

// Start performs DCR (loading cached creds first if available), exchanges for
// the initial token, and launches the refresh goroutine. The goroutine exits
// when ctx cancels.
func (a *Auth) Start(ctx context.Context) error {
	if err := a.ensureCreds(ctx); err != nil {
		return fmt.Errorf("DCR: %w", err)
	}
	tok, err := a.passwordGrant(ctx)
	if err != nil {
		return fmt.Errorf("initial token: %w", err)
	}
	a.setToken(tok)
	a.log.Info("apim auth ready",
		zap.String("scope", tok.Scope),
		zap.Time("expires_at", tok.ExpiresAt),
	)
	go a.refreshLoop(ctx)
	return nil
}

// GetToken returns a currently-valid access token. If the cached token is
// stale (refresh raced behind), it does an inline refresh.
func (a *Auth) GetToken(ctx context.Context) (string, error) {
	a.mu.RLock()
	tok := a.token
	a.mu.RUnlock()

	if tok != nil && time.Until(tok.ExpiresAt) > 30*time.Second {
		return tok.AccessToken, nil
	}

	// Fall back to a fresh issuance.
	fresh, err := a.passwordGrant(ctx)
	if err != nil {
		return "", err
	}
	a.setToken(fresh)
	return fresh.AccessToken, nil
}

// ensureCreds loads cached creds from disk or runs DCR and persists.
func (a *Auth) ensureCreds(ctx context.Context) error {
	if a.credsP != "" {
		if data, err := os.ReadFile(a.credsP); err == nil {
			var c Credentials
			if json.Unmarshal(data, &c) == nil && c.ClientID != "" {
				a.creds = &c
				a.log.Info("loaded cached DCR credentials", zap.String("path", a.credsP))
				return nil
			}
		}
	}

	creds, err := a.runDCR(ctx)
	if err != nil {
		return err
	}
	a.creds = creds

	if a.credsP != "" {
		if err := os.MkdirAll(filepath.Dir(a.credsP), 0o700); err != nil {
			a.log.Warn("create creds dir", zap.Error(err))
		}
		data, _ := json.Marshal(creds)
		if err := os.WriteFile(a.credsP, data, 0o600); err != nil {
			a.log.Warn("persist creds", zap.Error(err))
		} else {
			a.log.Info("DCR credentials persisted", zap.String("path", a.credsP))
		}
	}
	return nil
}

// runDCR posts the DCR registration. Uses Basic admin:admin per the spec.
func (a *Auth) runDCR(ctx context.Context) (*Credentials, error) {
	body, _ := json.Marshal(dcrRequest{
		CallbackURL: "www.example.com",
		ClientName:  "ads_client",
		Owner:       a.cfg.ServiceAccountUsername,
		GrantType:   "password refresh_token",
		SaaSApp:     true,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.cfg.PublisherURL+"/client-registration/v0.17/register", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+basic(a.cfg.ServiceAccountUsername, a.cfg.ServiceAccountPassword))

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DCR request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("DCR returned HTTP %d: %s", resp.StatusCode, snippet(respBody))
	}

	var dcr dcrResponse
	if err := json.Unmarshal(respBody, &dcr); err != nil {
		return nil, fmt.Errorf("DCR response decode: %w (body: %s)", err, snippet(respBody))
	}
	if dcr.ClientID == "" || dcr.ClientSecret == "" {
		return nil, fmt.Errorf("DCR response missing clientId/clientSecret: %s", snippet(respBody))
	}
	return &Credentials{ClientID: dcr.ClientID, ClientSecret: dcr.ClientSecret}, nil
}

// passwordGrant exchanges service-account creds for an access+refresh token.
// Implements the OAuth2 expiry guard per spec §3.3: if expires_in <= 60,
// the buffer is set to expires_in/3 (otherwise the standard 5-min buffer
// would yield a negative duration).
func (a *Auth) passwordGrant(ctx context.Context) (*Token, error) {
	form := url.Values{
		"grant_type": {"password"},
		"username":   {a.cfg.ServiceAccountUsername},
		"password":   {a.cfg.ServiceAccountPassword},
		"scope":      {"apim:api_view"},
	}
	return a.tokenRequest(ctx, form)
}

// tokenRequest is the shared body of password-grant and refresh-grant.
func (a *Auth) tokenRequest(ctx context.Context, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.cfg.PublisherURL+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+basic(a.creds.ClientID, a.creds.ClientSecret))

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("token decode: %w (body: %s)", err, snippet(body))
	}
	return &Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Scope:        tr.Scope,
		TokenType:    tr.TokenType,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}

// refreshLoop periodically refreshes the token. Uses the spec's expiry guard:
// normally refresh 5 minutes before expiry; if expires_in <= 60, refresh at
// expires_in/3.
func (a *Auth) refreshLoop(ctx context.Context) {
	for {
		a.mu.RLock()
		tok := a.token
		a.mu.RUnlock()
		if tok == nil {
			return
		}

		// Compute the sleep until the next refresh moment.
		ttl := time.Until(tok.ExpiresAt)
		buffer := 5 * time.Minute
		if ttl <= 60*time.Second {
			buffer = ttl / 3
		}
		sleep := ttl - buffer
		if sleep <= 0 {
			sleep = 1 * time.Second
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}

		fresh, err := a.refreshGrant(ctx, tok)
		if err != nil {
			a.log.Warn("token refresh failed; will retry next cycle", zap.Error(err))
			// Try a full re-issuance.
			fresh, err = a.passwordGrant(ctx)
			if err != nil {
				a.log.Error("token re-issuance failed", zap.Error(err))
				select {
				case <-ctx.Done():
					return
				case <-time.After(30 * time.Second):
				}
				continue
			}
		}
		a.setToken(fresh)
		a.log.Debug("token refreshed", zap.Time("expires_at", fresh.ExpiresAt))
	}
}

func (a *Auth) refreshGrant(ctx context.Context, prev *Token) (*Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {prev.RefreshToken},
		"scope":         {"apim:api_view"},
	}
	return a.tokenRequest(ctx, form)
}

func (a *Auth) setToken(t *Token) {
	a.mu.Lock()
	a.token = t
	a.mu.Unlock()
}

// basic returns the value to put after "Basic " in an Authorization header.
func basic(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// snippet limits an error body to 200 chars.
func snippet(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + "...(truncated)"
	}
	return string(b)
}

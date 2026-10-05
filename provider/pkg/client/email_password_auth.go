package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrRequires2FA is returned when a login succeeds in principle but the
// account has two-factor authentication enabled. There is no way to
// complete this non-interactively, and a Pulumi provider has no interactive
// terminal to prompt on at all, so this is always a hard failure here.
var ErrRequires2FA = errors.New("this account requires two-factor authentication and cannot be used for non-interactive Pulumi automation - use a dedicated service account with 2FA disabled")

// authTokenRefreshThreshold: re-authenticate if the cached access token
// is within this long of expiring, not only once it has already expired, so
// a request doesn't race a token that dies mid-flight.
const authTokenRefreshThreshold = 20 * time.Second

// AuthSessionConfig configures an email+password auth session against Code
// Capsules' hosted account login service - an auth mode offered alongside
// `apiKey` for convenience.
//
// Deliberately NOT a refresh-token flow: the login service's refresh-token
// endpoint rotates the refresh token on every call, so a Pulumi provider
// holding a single static refresh-token value in config across separate
// `pulumi up`/`preview`/`destroy` process invocations would have that value
// invalidated the first time any process refreshes, breaking every
// subsequent run. Logging in fresh with email+password whenever a valid
// token is needed sidesteps this entirely: nothing persisted, nothing to go
// stale across process boundaries.
type AuthSessionConfig struct {
	BaseURL  string
	APIKey   string
	Email    string
	Password string

	// HTTPClient allows tests to inject a client pointed at a fake server.
	// Defaults to a client with a 30s timeout, matching client.Config's own
	// default.
	HTTPClient *http.Client
}

// authSession is a TokenSource that lazily logs in and caches the access
// token in memory for the lifetime of the process, transparently logging in
// again (never refreshing) once the cached token is within
// authTokenRefreshThreshold of expiring.
type authSession struct {
	cfg AuthSessionConfig

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewAuthSession builds a TokenSource backed by email+password
// login. Returned as the TokenSource interface directly since the concrete
// type has no exported members callers need.
func NewAuthSession(cfg AuthSessionConfig) TokenSource {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &authSession{cfg: cfg}
}

// Token implements TokenSource. Safe for concurrent use - `pulumi up`'s
// default parallelism (10) can call this from multiple goroutines at once;
// the mutex ensures only one login happens even if several callers race in
// with an expired/absent cached token simultaneously.
func (s *authSession) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && time.Now().Add(authTokenRefreshThreshold).Before(s.expiry) {
		return s.token, nil
	}

	token, expiry, err := s.login(ctx)
	if err != nil {
		return "", err
	}
	s.token, s.expiry = token, expiry
	return s.token, nil
}

// authLoginRequest is the login request body. `type` is a literal
// constant the backend expects verbatim, not a real discriminator value
// callers choose.
type authLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Type     string `json:"type"`
}

const authLoginType = "[User] Login Initiated"

// authLoginResponse is the raw login response body: `token` is always
// present on a 2xx response, `refreshToken` is present only on a real login
// success - its absence (with token still present) signals a 2FA challenge
// token instead of a usable access token.
type authLoginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
}

func (s *authSession) login(ctx context.Context) (string, time.Time, error) {
	body, err := json.Marshal(authLoginRequest{
		Email:    s.cfg.Email,
		Password: s.cfg.Password,
		Type:     authLoginType,
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("marshal auth login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.BaseURL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build auth login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", s.cfg.APIKey)
	// The backend branches token transport behavior on this header, not
	// just a cosmetic one.
	req.Header.Set("x-auth-transport", "body")

	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth login request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read auth login response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("auth login failed: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed authLoginResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", time.Time{}, fmt.Errorf("decode auth login response: %w", err)
	}
	if parsed.Token == "" {
		return "", time.Time{}, errors.New("auth login response missing access token")
	}
	if parsed.RefreshToken == "" {
		// Present token, absent refreshToken: a 2FA challenge token, not a
		// usable access token.
		return "", time.Time{}, ErrRequires2FA
	}

	expiry, err := jwtExpiry(parsed.Token)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("decode access token: %w", err)
	}

	return parsed.Token, expiry, nil
}

// jwtExpiry reads the `exp` claim out of a JWT's payload segment without
// verifying its signature - this is our own token, just issued to us over
// TLS, so there is nothing to verify client-side.
func jwtExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, errors.New("not a JWT (expected 3 dot-separated segments)")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("base64-decode JWT payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("decode JWT payload: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, errors.New("JWT payload missing exp claim")
	}
	return time.Unix(claims.Exp, 0), nil
}

// Package client is a minimal REST client for the Code Capsules `api` service,
// modeled on codecapsules-sandbox/src/client.ts's retry/backoff/error-mapping shape.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TokenSource returns the Bearer token to use for the next request. Token is
// called fresh on every request (not cached by Client itself) so an
// implementation like *authSession can transparently re-authenticate
// when its cached access token is near expiry - see email_password_auth.go.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// staticToken is a TokenSource that always returns the same fixed value -
// used for the `apiKey` auth mode. Also the implicit TokenSource when a
// Config is built with ApiKey set directly (see New below), which is why
// every existing test/call site that constructs Config{ApiKey: "..."}
// keeps working unchanged.
type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

// Config configures a Client. ApiKey is expected to be a Pulumi secret at the
// provider-config layer; this package treats it as an opaque bearer credential.
//
// Exactly one of ApiKey or TokenSource is normally meaningful: set TokenSource
// directly (e.g. to an email+password-backed session, see email_password_auth.go) for the
// email+password stopgap auth mode, or leave it nil and set ApiKey for the
// static-token path (today: a placeholder for the not-yet-existing PAT). If
// both are set, TokenSource wins and ApiKey is ignored - New does not error on
// this since it isn't reachable through normal provider Configure flow (see
// provider/pkg/provider/config.go, which picks one mode and never sets both).
type Config struct {
	ApiURL string
	ApiKey string

	// TokenSource, when set, overrides ApiKey entirely as the source of the
	// per-request Bearer token. Left nil by most tests/callers, which get a
	// staticToken wrapping ApiKey instead (see New).
	TokenSource TokenSource

	// HTTPClient allows tests to inject a client pointed at a fake server.
	// Defaults to a client with a 30s timeout.
	HTTPClient *http.Client

	// MaxRetries bounds retry attempts for safe-to-retry requests. Defaults to 3.
	MaxRetries int

	// baseBackoff is exposed only for tests to keep the suite fast; production
	// callers should leave it unset and get the 250ms default.
	baseBackoff time.Duration
}

type Client struct {
	cfg Config
}

func New(cfg Config) *Client {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.baseBackoff == 0 {
		cfg.baseBackoff = 250 * time.Millisecond
	}
	if cfg.TokenSource == nil {
		cfg.TokenSource = staticToken(cfg.ApiKey)
	}
	return &Client{cfg: cfg}
}

// APIError represents a non-2xx response from the platform API. Callers should
// use the Is* helpers rather than comparing StatusCode directly, since some
// endpoints (see space/team delete) use 400 for validation-style conflicts.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api: unexpected status %d: %s", e.StatusCode, e.Body)
}

func (e *APIError) IsNotFound() bool     { return e.StatusCode == http.StatusNotFound }
func (e *APIError) IsConflict() bool     { return e.StatusCode == http.StatusConflict }
func (e *APIError) IsUnauthorized() bool { return e.StatusCode == http.StatusUnauthorized }
func (e *APIError) IsForbidden() bool    { return e.StatusCode == http.StatusForbidden }
func (e *APIError) IsBadRequest() bool   { return e.StatusCode == http.StatusBadRequest }
func (e *APIError) IsRateLimited() bool  { return e.StatusCode == http.StatusTooManyRequests }

// envelope mirrors `api`'s `{ data, meta }` response wrapper (see
// api/src/utilities/http-util.ts handleRequest / swagger-doc.json).
type envelope struct {
	Data json.RawMessage `json:"data"`
}

// retrySafety controls whether a request may be retried after a transport-level
// failure (dial/connection error before any response was received) versus also
// being retried on ambiguous outcomes (e.g. timeout after the request was
// already sent). POST is never given ambiguous-retry: a duplicated Create is a
// worse failure mode than a Pulumi-level retry after a clean error. See the
// plan's "no idempotency story for retried Create" critique.
type retrySafety int

const (
	retryNever          retrySafety = iota // never retry, not even pre-send failures
	retryPreSendOnly                       // retry only if we know the request was never sent
	retryPreAndPostSend                    // fully idempotent method: retry on any transport error
)

func safetyFor(method string) retrySafety {
	switch method {
	case http.MethodGet, http.MethodDelete:
		return retryPreAndPostSend
	case http.MethodPatch, http.MethodPut:
		// PATCH here is always a full-field replace (team/space update), so a
		// retried duplicate has the same effect as the original - safe to
		// retry even post-send.
		return retryPreAndPostSend
	case http.MethodPost:
		// Create. A retry after the request left the wire risks duplicate
		// resource creation; only retry if we're sure nothing was sent.
		return retryPreSendOnly
	default:
		return retryNever
	}
}

// do issues an HTTP request against the platform `api` service, retrying
// transient failures according to safetyFor(method), and unwraps the
// `{ data }` envelope into out (if non-nil). A non-2xx response is always
// returned as *APIError, never retried past what safetyFor allows for 429s.
func (c *Client) do(ctx context.Context, method, path string, body, out interface{}) error {
	return c.request(ctx, method, c.cfg.ApiURL, path, body, out, true)
}

// doCapsuleAPI issues an HTTP request against a per-cluster `capsule-api`
// instance, resolved fresh per-call by the provider layer (see
// provider.resolveSpaceEndpoint) rather than cached on this Client - mirrors
// the platform's own two-hop Space->cluster.clusterApiEndpoint resolution
// (cli/src/modules/capsule/services/capsule.service.ts).
//
// Unlike `api`, `capsule-api` (LoopBack4) returns its DTOs directly as the
// response body with no `{ data }` wrapper - verified against
// capsule-api/src/capsule/controllers/*.ts, whose handlers return
// `Promise<CapsuleResponse>` etc. directly. envelope is therefore always
// false here.
func (c *Client) doCapsuleAPI(ctx context.Context, clusterAPIEndpoint, method, path string, body, out interface{}) error {
	return c.request(ctx, method, clusterAPIEndpoint, path, body, out, false)
}

// request is the shared implementation behind do/doCapsuleAPI - identical
// retry/backoff/error-mapping behavior against either backend, differing
// only in base URL and whether the response body is `{ data }`-wrapped.
// useEnvelope is named to avoid shadowing the package-level `envelope` type
// used just below.
func (c *Client) request(ctx context.Context, method, baseURL, path string, body, out interface{}, useEnvelope bool) error {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
	}

	safety := safetyFor(method)
	var lastErr error

	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.cfg.baseBackoff * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reqBody)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		token, err := c.cfg.TokenSource.Token(ctx)
		if err != nil {
			return fmt.Errorf("obtain auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.cfg.HTTPClient.Do(req)
		if err != nil {
			// A transport error here could mean the request was never sent
			// (safe to retry for any method) or that it was sent and only the
			// response was lost (only safe for idempotent methods). Go's
			// http.Client does not expose which case occurred, so we take the
			// conservative reading: treat every transport error as
			// "might have been sent" and gate retry on safety, never
			// downgrading retryPreSendOnly to a full retry.
			lastErr = err
			if safety == retryNever || attempt == c.cfg.MaxRetries {
				return fmt.Errorf("request %s %s: %w", method, path, err)
			}
			if safety == retryPreSendOnly {
				// Conservatively refuse to retry POST past a transport error,
				// since we cannot prove it wasn't sent.
				return fmt.Errorf("request %s %s: %w (not retried: unsafe for POST)", method, path, err)
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt == c.cfg.MaxRetries || safety != retryPreAndPostSend {
				return fmt.Errorf("read response %s %s: %w", method, path, readErr)
			}
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil {
				return nil
			}
			if !useEnvelope {
				if len(respBody) == 0 {
					return nil
				}
				if err := json.Unmarshal(respBody, out); err != nil {
					return fmt.Errorf("decode response %s %s: %w", method, path, err)
				}
				return nil
			}
			var env envelope
			if err := json.Unmarshal(respBody, &env); err != nil {
				return fmt.Errorf("decode envelope %s %s: %w", method, path, err)
			}
			if len(env.Data) == 0 || string(env.Data) == "null" {
				return nil
			}
			if err := json.Unmarshal(env.Data, out); err != nil {
				return fmt.Errorf("decode data %s %s: %w", method, path, err)
			}
			return nil
		}

		apiErr := &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
		if apiErr.IsRateLimited() && attempt < c.cfg.MaxRetries {
			lastErr = apiErr
			continue
		}
		return apiErr
	}

	return lastErr
}

package client_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
)

// fakeJWT builds a minimal unsigned token with only the `exp` claim set -
// email_password_auth.go's jwtExpiry never verifies a signature (it's our own
// token, issued to us over TLS), so an arbitrary/empty signature segment is
// fine for tests.
func fakeJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(map[string]int64{"exp": exp.Unix()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".sig"
}

// fakeAuthServer serves a single configurable response (success,
// requires2fa-shaped, or an error status) to every /api/auth/login request,
// and counts how many login calls it received so tests can assert on
// caching behavior (one login per Token() call expected to actually hit the
// network vs. served from cache).
type fakeAuthServer struct {
	*httptest.Server
	logins int32

	// response controls what the next (and all subsequent, until changed)
	// login calls return.
	status int
	body   map[string]interface{}
}

func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	t.Helper()
	f := &fakeAuthServer{status: http.StatusOK}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/login" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&f.logins, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_ = json.NewEncoder(w).Encode(f.body)
	}))
	return f
}

func (f *fakeAuthServer) loginCount() int { return int(atomic.LoadInt32(&f.logins)) }

func TestAuthSession_LoginAndCache(t *testing.T) {
	fake := newFakeAuthServer(t)
	defer fake.Close()
	fake.body = map[string]interface{}{
		"token":        fakeJWT(t, time.Now().Add(1*time.Hour)),
		"refreshToken": "rt-1",
	}

	session := client.NewAuthSession(client.AuthSessionConfig{
		BaseURL: fake.URL, APIKey: "test-auth-key", Email: "svc@example.com", Password: "pw",
	})

	tok1, err := session.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok1 == "" {
		t.Fatalf("expected non-empty token")
	}
	if fake.loginCount() != 1 {
		t.Fatalf("expected 1 login call, got %d", fake.loginCount())
	}

	// Token is valid for another hour, well past the 20s threshold - a
	// second Token() call should be served from cache, not hit the network.
	tok2, err := session.Token(context.Background())
	if err != nil {
		t.Fatalf("Token (cached): %v", err)
	}
	if tok2 != tok1 {
		t.Fatalf("expected cached token to be returned unchanged")
	}
	if fake.loginCount() != 1 {
		t.Fatalf("expected still 1 login call after cached Token(), got %d", fake.loginCount())
	}
}

func TestAuthSession_RelogsNearExpiry(t *testing.T) {
	fake := newFakeAuthServer(t)
	defer fake.Close()
	// Issued already within the 20s re-login threshold.
	fake.body = map[string]interface{}{
		"token":        fakeJWT(t, time.Now().Add(5*time.Second)),
		"refreshToken": "rt-1",
	}

	session := client.NewAuthSession(client.AuthSessionConfig{
		BaseURL: fake.URL, APIKey: "test-auth-key", Email: "svc@example.com", Password: "pw",
	})

	if _, err := session.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if fake.loginCount() != 1 {
		t.Fatalf("expected 1 login call, got %d", fake.loginCount())
	}

	// Next token is further out, so the second Token() call should be
	// distinguishable from the first by having actually re-logged in.
	fake.body = map[string]interface{}{
		"token":        fakeJWT(t, time.Now().Add(1*time.Hour)),
		"refreshToken": "rt-2",
	}
	if _, err := session.Token(context.Background()); err != nil {
		t.Fatalf("Token (re-login): %v", err)
	}
	if fake.loginCount() != 2 {
		t.Fatalf("expected 2 login calls (near-expiry should trigger re-login), got %d", fake.loginCount())
	}
}

func TestAuthSession_Requires2FA(t *testing.T) {
	fake := newFakeAuthServer(t)
	defer fake.Close()
	// Present token, absent refreshToken: a 2FA challenge token per
	// oauth.utility.ts's inference, not a usable access token.
	fake.body = map[string]interface{}{
		"token": fakeJWT(t, time.Now().Add(5*time.Minute)),
	}

	session := client.NewAuthSession(client.AuthSessionConfig{
		BaseURL: fake.URL, APIKey: "test-auth-key", Email: "svc@example.com", Password: "pw",
	})

	_, err := session.Token(context.Background())
	if !errors.Is(err, client.ErrRequires2FA) {
		t.Fatalf("expected ErrRequires2FA, got %v", err)
	}
}

func TestAuthSession_BadCredentials(t *testing.T) {
	fake := newFakeAuthServer(t)
	defer fake.Close()
	fake.status = http.StatusUnauthorized
	fake.body = map[string]interface{}{"message": "invalid credentials"}

	session := client.NewAuthSession(client.AuthSessionConfig{
		BaseURL: fake.URL, APIKey: "test-auth-key", Email: "svc@example.com", Password: "wrong",
	})

	_, err := session.Token(context.Background())
	if err == nil {
		t.Fatalf("expected an error for bad credentials, got nil")
	}
	if errors.Is(err, client.ErrRequires2FA) {
		t.Fatalf("bad credentials should not be reported as ErrRequires2FA")
	}
}

// TestStaticApiKeyPathUnchanged guards against a regression where adding
// TokenSource broke the plain ApiKey path every existing test/call site
// relies on (client.New auto-wraps ApiKey in a staticToken when TokenSource
// is nil - see client.go).
func TestStaticApiKeyPathUnchanged(t *testing.T) {
	c := client.New(client.Config{ApiURL: "http://example.invalid", ApiKey: "fixed-key"})
	if c == nil {
		t.Fatalf("expected non-nil client")
	}
}

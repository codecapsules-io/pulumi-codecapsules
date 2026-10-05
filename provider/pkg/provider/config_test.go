package provider_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/provider"
)

func fakeAuthJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]int64{"exp": exp.Unix()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// newFakeAuthLoginServer serves one fixed response to every
// /api/auth/login request - enough for Configure-level tests, which only
// need to exercise the mode-selection and fail-fast behavior, not the
// caching/re-login behavior already covered by client/email_password_auth_test.go.
func newFakeAuthLoginServer(status int, body map[string]interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestConfigure_EmailPasswordMode(t *testing.T) {
	fake := newFakeAuthLoginServer(http.StatusOK, map[string]interface{}{
		"token":        fakeAuthJWT(t, time.Now().Add(time.Hour)),
		"refreshToken": "rt-1",
	})
	defer fake.Close()

	cfg := &provider.Config{
		AuthBaseUrl: fake.URL,
		AuthApiKey:  "test-auth-key",
		Email:       "svc@example.com",
		Password:    "pw",
	}
	if err := cfg.Configure(context.Background()); err != nil {
		t.Fatalf("Configure: %v", err)
	}
}

func TestConfigure_EmailPasswordMode_BadCredentialsFailsFast(t *testing.T) {
	fake := newFakeAuthLoginServer(http.StatusUnauthorized, map[string]interface{}{"message": "invalid"})
	defer fake.Close()

	cfg := &provider.Config{
		AuthBaseUrl: fake.URL,
		AuthApiKey:  "test-auth-key",
		Email:       "svc@example.com",
		Password:    "wrong",
	}
	if err := cfg.Configure(context.Background()); err == nil {
		t.Fatalf("expected Configure to fail fast on bad credentials")
	}
}

func TestConfigure_EmailPasswordMode_Requires2FAFailsFast(t *testing.T) {
	fake := newFakeAuthLoginServer(http.StatusOK, map[string]interface{}{
		// token present, refreshToken absent => 2FA challenge, not success.
		"token": fakeAuthJWT(t, time.Now().Add(5*time.Minute)),
	})
	defer fake.Close()

	cfg := &provider.Config{
		AuthBaseUrl: fake.URL,
		AuthApiKey:  "test-auth-key",
		Email:       "svc@example.com",
		Password:    "pw",
	}
	err := cfg.Configure(context.Background())
	if err == nil {
		t.Fatalf("expected Configure to fail fast for a 2FA-enabled account")
	}
}

func TestConfigure_NeitherModeSetFailsFast(t *testing.T) {
	cfg := &provider.Config{}
	if err := cfg.Configure(context.Background()); err == nil {
		t.Fatalf("expected Configure to error when neither apiKey nor email/password is set")
	}
}

func TestConfigure_ApiKeyModeStillWorks(t *testing.T) {
	cfg := &provider.Config{ApiKey: "test-key"}
	if err := cfg.Configure(context.Background()); err != nil {
		t.Fatalf("Configure: %v", err)
	}
}

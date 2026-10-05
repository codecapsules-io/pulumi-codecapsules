package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
)

const defaultApiURL = "https://api.codecapsules.io"

// defaultAuthBaseURL/APIKey point at Code Capsules' hosted account login
// service, used by the email/password auth mode below. The API key here
// identifies the calling application, not a per-user secret - safe to
// default, but still overridable (authBaseUrl/authApiKey) for pointing at a
// non-production environment.
const (
	defaultAuthBaseURL = "https://appstrax-services.codecapsules.io"
	defaultAuthAPIKey  = "0Z2fQSYOqrnlJAI5zUkCCrJJOvT6y8"
)

// Config is the provider-level configuration block.
//
// Two mutually exclusive auth modes, picked in Configure:
//
//  1. apiKey: an API key issued for your Code Capsules account.
//  2. email+password: sign in with your Code Capsules account credentials.
//     Logs in fresh whenever a valid access token is needed, rather than
//     holding a long-lived session - see client/email_password_auth.go's doc
//     comment for why. Accounts with two-factor authentication enabled
//     cannot use this mode (client.ErrRequires2FA) - use an apiKey, or a
//     dedicated account without 2FA, instead.
//
// If both email and password are set, that mode wins over apiKey (chosen as
// the explicit, deliberately-configured mode over the placeholder-shaped
// one). If neither apiKey nor email/password are set, Configure fails fast
// with a clear error rather than silently building a client with an empty
// bearer token that would only surface as a confusing 401 on first use.
type Config struct {
	ApiUrl string `pulumi:"apiUrl,optional"`
	// Secret-ness must be declared in the `provider` tag namespace, not
	// `pulumi` - see pulumi/pulumi-go-provider#192. Putting `secret` in the
	// `pulumi` tag instead panics at runtime the moment Configure runs,
	// since provider-config decode goes through a stricter legacy mapper
	// that only understands `optional`/`omitempty`/`skip` in that namespace.
	ApiKey string `pulumi:"apiKey,optional" provider:"secret"`

	Email       string `pulumi:"email,optional"`
	Password    string `pulumi:"password,optional" provider:"secret"`
	AuthBaseUrl string `pulumi:"authBaseUrl,optional"`
	AuthApiKey  string `pulumi:"authApiKey,optional" provider:"secret"`

	client *client.Client
}

// Configure builds the internal REST client once, after inputs are decoded.
// See infer.CustomConfigure: this mutates the receiver that infer.GetConfig
// later returns copies of, so client() below sees the built client on every
// subsequent resource call within this provider process.
func (c *Config) Configure(ctx context.Context) error {
	apiURL := c.ApiUrl
	if apiURL == "" {
		apiURL = defaultApiURL
	}

	cfg := client.Config{ApiURL: apiURL}

	switch {
	case c.Email != "" && c.Password != "":
		authBaseURL := c.AuthBaseUrl
		if authBaseURL == "" {
			authBaseURL = defaultAuthBaseURL
		}
		authAPIKey := c.AuthApiKey
		if authAPIKey == "" {
			authAPIKey = defaultAuthAPIKey
		}
		session := client.NewAuthSession(client.AuthSessionConfig{
			BaseURL:  authBaseURL,
			APIKey:   authAPIKey,
			Email:    c.Email,
			Password: c.Password,
		})
		// Fail fast on bad credentials/2FA here rather than surfacing a
		// confusing error on the first resource operation.
		if _, err := session.Token(ctx); err != nil {
			if errors.Is(err, client.ErrRequires2FA) {
				return err
			}
			return fmt.Errorf("auth login failed: %w", err)
		}
		cfg.TokenSource = session
	case c.ApiKey != "":
		cfg.ApiKey = c.ApiKey
	default:
		return errors.New("codecapsules provider: either apiKey, or both email and password, must be set")
	}

	c.client = client.New(cfg)
	return nil
}

func (c Config) mustClient() *client.Client {
	if c.client == nil {
		// Only reachable if a resource method runs before Configure, which
		// infer's provider wiring does not allow in practice - fail loudly
		// rather than silently talking to no API.
		panic("codecapsules provider: client requested before Configure ran")
	}
	return c.client
}

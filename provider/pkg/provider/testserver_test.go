package provider_test

import (
	"context"
	"testing"

	"github.com/blang/semver"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/provider"
	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/testutil"
)

// newTestServer builds the real codecapsules provider (the exact one the
// shipped binary serves - see provider.Provider()) behind
// pulumi-go-provider's gRPC-level integration.Server test harness, wired to
// talk to a fake in-memory backend. This is what the plan's "use
// providertest-style lifecycle tests, not just mocked HTTP unit tests"
// critique asked for: Create/Read/Update/Delete/Diff are exercised exactly
// as the Pulumi engine would call them, through Check/Diff-driven
// LifeCycleTest runs.
func newTestServer(t *testing.T, fake *testutil.FakeServer) integration.Server {
	t.Helper()

	prov, err := provider.Provider()
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}

	server, err := integration.NewServer(context.Background(), "codecapsules", semver.MustParse("0.1.0"),
		integration.WithProvider(prov))
	if err != nil {
		t.Fatalf("build test server: %v", err)
	}

	err = server.Configure(p.ConfigureRequest{
		Args: property.NewMap(map[string]property.Value{
			"apiUrl": property.New(fake.URL),
			"apiKey": property.New("test-key"),
		}),
	})
	if err != nil {
		t.Fatalf("configure provider: %v", err)
	}
	return server
}

package provider

// White-box tests for pollDataCapsuleReady/resolveSpaceEndpoint,
// deliberately in `package provider` (not `provider_test`) so they can
// shrink the unexported capsulePollInterval/MaxInterval/Timeout vars
// directly - the gRPC-level LifeCycleTest harness used elsewhere
// (mysql_capsule_test.go etc.) has no way to reach into an in-flight
// Create's polling, and the fake server's every-capsule-Ready-by-default
// behavior means a lifecycle test alone never actually exercises the
// multi-poll path.

import (
	"context"
	"testing"
	"time"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/testutil"
)

// withFastPoll shrinks the package-level poll durations for one test,
// restoring the production defaults on cleanup.
func withFastPoll(t *testing.T, interval, timeout time.Duration) {
	t.Helper()
	origInterval, origMax, origTimeout := capsulePollInterval, capsulePollMaxInterval, capsulePollTimeout
	capsulePollInterval = interval
	capsulePollMaxInterval = interval
	capsulePollTimeout = timeout
	t.Cleanup(func() {
		capsulePollInterval, capsulePollMaxInterval, capsulePollTimeout = origInterval, origMax, origTimeout
	})
}

func TestPollDataCapsuleReadySlowThenReady(t *testing.T) {
	withFastPoll(t, 10*time.Millisecond, time.Second)

	fake := testutil.New()
	defer fake.Close()
	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")

	c := client.New(client.Config{ApiURL: fake.URL, ApiKey: "test-key"})
	ctx := context.Background()

	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, space.ID)
	if err != nil {
		t.Fatalf("resolveSpaceEndpoint: %v", err)
	}

	manifest := map[string]interface{}{"manifestType": "data", "dataType": "mysql", "name": "slow-db"}
	capsule, err := c.CreateCapsule(ctx, endpoint, namespaceKey, "slow-db", "", manifest, client.ProductsInput{})
	if err != nil {
		t.Fatalf("CreateCapsule: %v", err)
	}
	// Two Starting responses before Ready - proves the loop actually polls
	// more than once, not just that an immediately-Ready fake passes.
	fake.SetCapsuleReadyAfter(capsule.ID, 2)

	ready, err := pollDataCapsuleReady(ctx, c, endpoint, capsule.ID)
	if err != nil {
		t.Fatalf("pollDataCapsuleReady: %v", err)
	}
	if ready.Status() != capsuleStatusReady {
		t.Fatalf("expected status %q, got %q", capsuleStatusReady, ready.Status())
	}
}

func TestPollDataCapsuleReadyTimesOut(t *testing.T) {
	withFastPoll(t, 5*time.Millisecond, 30*time.Millisecond)

	fake := testutil.New()
	defer fake.Close()
	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")

	c := client.New(client.Config{ApiURL: fake.URL, ApiKey: "test-key"})
	ctx := context.Background()

	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, space.ID)
	if err != nil {
		t.Fatalf("resolveSpaceEndpoint: %v", err)
	}

	manifest := map[string]interface{}{"manifestType": "data", "dataType": "mysql", "name": "stuck-db"}
	capsule, err := c.CreateCapsule(ctx, endpoint, namespaceKey, "stuck-db", "", manifest, client.ProductsInput{})
	if err != nil {
		t.Fatalf("CreateCapsule: %v", err)
	}
	// Never satisfied within the 30ms timeout configured above - this is
	// the real API's actual shape (no "Failed" status; a stuck capsule just
	// never reports Ready), so a timeout is the only failure this can ever
	// detect, by design.
	fake.SetCapsuleReadyAfter(capsule.ID, 1000)

	if _, err := pollDataCapsuleReady(ctx, c, endpoint, capsule.ID); err == nil {
		t.Fatalf("expected pollDataCapsuleReady to time out, got nil error")
	}
}

func TestResolveSpaceEndpointMissingCluster(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "")
	space.Cluster.ClusterApiEndpoint = "" // simulate an unresolvable cluster

	c := client.New(client.Config{ApiURL: fake.URL, ApiKey: "test-key"})
	if _, _, err := resolveSpaceEndpoint(context.Background(), c, space.ID); err == nil {
		t.Fatalf("expected an error when the space's cluster endpoint can't be resolved")
	}
}

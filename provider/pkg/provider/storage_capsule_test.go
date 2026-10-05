package provider_test

import (
	"testing"

	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/testutil"
)

func storageInputs(spaceID, name string) property.Map {
	return property.NewMap(map[string]property.Value{
		"spaceId":    property.New(spaceID),
		"name":       property.New(name),
		"cpuQty":     property.New(50.0),
		"memoryQty":  property.New(64.0),
		"storageQty": property.New(5.0),
		"replicas":   property.New(1.0),
	})
}

// TestStorageCapsuleLifecycle proves Create does not poll (StorageCapsule's
// doc comment: persistent-storage.ts's create() sets status Ready
// synchronously) - if this accidentally started polling against a fake
// that never advances pollsUntilReady for PersistentStorage, Create would
// hang until capsulePollTimeout; this test's default timeout failing fast
// is itself part of the proof.
func TestStorageCapsuleLifecycle(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")

	test := integration.LifeCycleTest{
		Resource: "codecapsules:index:StorageCapsule",
		Create:   integration.Operation{Inputs: storageInputs(space.ID, "my-storage")},
		Updates: []integration.Operation{
			{
				Inputs: storageInputs(space.ID, "my-storage").Set("storageQty", property.New(20.0)),
				Hook: func(_, output property.Map) {
					if got := output.Get("storageQty").AsNumber(); got != 20.0 {
						t.Fatalf("expected updated storageQty 20, got %v", got)
					}
				},
			},
		},
	}
	test.Run(t, server)
}

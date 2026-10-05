package provider_test

import (
	"testing"

	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/testutil"
)

func redisInputs(spaceID, name string) property.Map {
	return property.NewMap(map[string]property.Value{
		"spaceId":    property.New(spaceID),
		"name":       property.New(name),
		"cpuQty":     property.New(100.0),
		"memoryQty":  property.New(128.0),
		"storageQty": property.New(1.0),
		"replicas":   property.New(1.0),
	})
}

// TestRedisCapsuleLifecycle mirrors TestMysqlCapsuleLifecycle - same
// create/poll-to-Ready/in-place-sizing-update shape, proving RedisCapsule
// didn't silently diverge from MysqlCapsule's behavior.
func TestRedisCapsuleLifecycle(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")

	test := integration.LifeCycleTest{
		Resource: "codecapsules:index:RedisCapsule",
		Create: integration.Operation{
			Inputs: redisInputs(space.ID, "my-cache"),
			Hook: func(_, output property.Map) {
				if got := output.Get("privateConnectionString").AsString(); got == "" {
					t.Fatalf("expected server-assigned privateConnectionString on create")
				}
			},
		},
		Updates: []integration.Operation{
			{
				Inputs: redisInputs(space.ID, "my-cache").Set("memoryQty", property.New(256.0)),
				Hook: func(_, output property.Map) {
					if got := output.Get("memoryQty").AsNumber(); got != 256.0 {
						t.Fatalf("expected updated memoryQty 256, got %v", got)
					}
				},
			},
		},
	}
	test.Run(t, server)
}

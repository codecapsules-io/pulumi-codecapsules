package provider_test

import (
	"testing"

	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/testutil"
)

func mysqlInputs(spaceID, name string) property.Map {
	return property.NewMap(map[string]property.Value{
		"spaceId":    property.New(spaceID),
		"name":       property.New(name),
		"cpuQty":     property.New(100.0),
		"memoryQty":  property.New(128.0),
		"storageQty": property.New(1.0),
		"replicas":   property.New(1.0),
	})
}

// TestMysqlCapsuleLifecycle proves Create returns only once the fake
// backend's GET /data-capsule/{id}/status reports Ready (the fake defaults
// every new data capsule to Ready on its first poll, so this doesn't wait
// in real time) and that sizing/description changes update in place rather
// than replacing. The actual multi-poll "slow-then-ready" path is unit
// tested directly against pollDataCapsuleReady in capsule_poll_test.go,
// since that needs to shrink the poll interval to keep the test fast - not
// expressible through this gRPC-level harness alone.
func TestMysqlCapsuleLifecycle(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")

	test := integration.LifeCycleTest{
		Resource: "codecapsules:index:MysqlCapsule",
		Create: integration.Operation{
			Inputs: mysqlInputs(space.ID, "my-db"),
			Hook: func(_, output property.Map) {
				if got := output.Get("privateConnectionString").AsString(); got == "" {
					t.Fatalf("expected server-assigned privateConnectionString on create")
				}
				if got := output.Get("spaceId").AsString(); got != space.ID {
					t.Fatalf("expected spaceId %q, got %q", space.ID, got)
				}
			},
		},
		Updates: []integration.Operation{
			{
				// description-only: in-place update.
				Inputs: mysqlInputs(space.ID, "my-db").Set("description", property.New("updated")),
				Hook: func(_, output property.Map) {
					if got := output.Get("description").AsString(); got != "updated" {
						t.Fatalf("expected updated description, got %q", got)
					}
				},
			},
			{
				// sizing-only: in-place update via products patch, not a replace.
				Inputs: mysqlInputs(space.ID, "my-db").
					Set("description", property.New("updated")).
					Set("memoryQty", property.New(256.0)),
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

// TestMysqlCapsuleNameChangeReplaces proves a name change is modeled as
// UpdateReplace, not an in-place Update - capsule-api has no rename
// endpoint (confirmed: the generic PATCH .../capsule/{id} path only writes
// `description`).
func TestMysqlCapsuleNameChangeReplaces(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")

	test := integration.LifeCycleTest{
		Resource: "codecapsules:index:MysqlCapsule",
		Create:   integration.Operation{Inputs: mysqlInputs(space.ID, "my-db")},
		Updates: []integration.Operation{
			{
				Inputs: mysqlInputs(space.ID, "renamed-db"),
				Hook: func(_, output property.Map) {
					if got := output.Get("name").AsString(); got != "renamed-db" {
						t.Fatalf("expected replaced name 'renamed-db', got %q", got)
					}
				},
			},
		},
	}
	test.Run(t, server)
}

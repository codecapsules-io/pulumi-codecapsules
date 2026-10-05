package provider_test

import (
	"testing"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	presource "github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/testutil"
)

func wordpressInputs(spaceID, name, mysqlID, storageID string) property.Map {
	return property.NewMap(map[string]property.Value{
		"spaceId":          property.New(spaceID),
		"name":             property.New(name),
		"version":          property.New("6.5"),
		"mysqlCapsuleId":   property.New(mysqlID),
		"databaseName":     property.New("wordpress"),
		"storageCapsuleId": property.New(storageID),
		"cpuQty":           property.New(100.0),
		"memoryQty":        property.New(128.0),
		"storageQty":       property.New(1.0),
		"replicas":         property.New(1.0),
	})
}

// createFixtureCapsule drives the given resource's Create directly through
// the gRPC harness (not LifeCycleTest, which only runs one resource type at
// a time) to get a real id for WordPress to reference - mirrors how a
// consuming Pulumi program would create the Mysql/StorageCapsule
// prerequisites before the WordpressCapsule that depends on them.
func createFixtureCapsule(t *testing.T, server integration.Server, resourceType string, inputs property.Map) string {
	t.Helper()
	urn := presource.NewURN("test", "provider", "", tokens.Type(resourceType), "fixture")
	resp, err := server.Create(p.CreateRequest{Urn: urn, Properties: inputs, DryRun: false})
	if err != nil {
		t.Fatalf("create fixture %s: %v", resourceType, err)
	}
	return resp.ID
}

// TestWordpressCapsuleLifecycle proves the id-only linking to an
// already-created Mysql/StorageCapsule (no host/port/credential copying -
// see WordpressManifestRequest.storageCapsuleId/database.mysqlCapsuleId)
// and that env changes go through SetCapsuleConfigs rather than a replace.
func TestWordpressCapsuleLifecycle(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")

	mysqlID := createFixtureCapsule(t, server, "codecapsules:index:MysqlCapsule", mysqlInputs(space.ID, "wp-db"))
	storageID := createFixtureCapsule(t, server, "codecapsules:index:StorageCapsule", storageInputs(space.ID, "wp-storage"))

	test := integration.LifeCycleTest{
		Resource: "codecapsules:index:WordpressCapsule",
		Create: integration.Operation{
			Inputs: wordpressInputs(space.ID, "my-site", mysqlID, storageID),
			Hook: func(_, output property.Map) {
				if got := output.Get("hostname").AsString(); got == "" {
					t.Fatalf("expected server-assigned hostname on create")
				}
				if got := output.Get("mysqlCapsuleId").AsString(); got != mysqlID {
					t.Fatalf("expected mysqlCapsuleId %q, got %q", mysqlID, got)
				}
			},
		},
		Updates: []integration.Operation{
			{
				// env-only: in-place update via SetCapsuleConfigs, not a replace.
				Inputs: wordpressInputs(space.ID, "my-site", mysqlID, storageID).
					Set("env", property.New(property.NewMap(map[string]property.Value{
						"REDIS_HOST": property.New("my-cache-redis"),
					}))),
				Hook: func(_, output property.Map) {
					env := output.Get("env").AsMap()
					if got := env.Get("REDIS_HOST").AsString(); got != "my-cache-redis" {
						t.Fatalf("expected env REDIS_HOST to be updated, got %q", got)
					}
				},
			},
		},
	}
	test.Run(t, server)
}

// TestWordpressCapsuleRequiresVersionForDefaultDeployment documents
// wordpress-cargo.ts's isManifestValid: version is required when
// deploymentType is "default" (the only deploymentType this resource
// supports).
func TestWordpressCapsuleRequiresVersionForDefaultDeployment(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")

	mysqlID := createFixtureCapsule(t, server, "codecapsules:index:MysqlCapsule", mysqlInputs(space.ID, "wp-db"))
	storageID := createFixtureCapsule(t, server, "codecapsules:index:StorageCapsule", storageInputs(space.ID, "wp-storage"))

	inputs := wordpressInputs(space.ID, "my-site", mysqlID, storageID).Set("version", property.New(""))
	urn := presource.NewURN("test", "provider", "", "codecapsules:index:WordpressCapsule", "test")
	if _, err := server.Create(p.CreateRequest{Urn: urn, Properties: inputs, DryRun: false}); err == nil {
		t.Fatalf("expected Create to fail without a version")
	}
}

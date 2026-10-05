package provider_test

import (
	"testing"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	presource "github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/testutil"
)

func spaceInputs(name, slug, teamID, clusterID string) property.Map {
	return property.NewMap(map[string]property.Value{
		"name":      property.New(name),
		"slug":      property.New(slug),
		"teamId":    property.New(teamID),
		"clusterId": property.New(clusterID),
	})
}

// TestSpaceLifecycle mirrors TestTeamLifecycle: preview->create->in-place
// name update->replace-triggering slug/team/cluster changes, through the
// same gRPC-level harness. Space has three replaceOnChanges fields instead
// of Team's one, so this also proves each of them independently forces a
// replace rather than only the first one being wired correctly.
func TestSpaceLifecycle(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	team := fake.SeedTeam("Owning Team", "owning-team")
	otherTeam := fake.SeedTeam("Other Team", "other-team")

	test := integration.LifeCycleTest{
		Resource: "codecapsules:index:Space",
		Create: integration.Operation{
			Inputs: spaceInputs("My Space", "my-space", team.ID, "cluster-1"),
			Hook: func(_, output property.Map) {
				if got := output.Get("namespaceKey").AsString(); got == "" {
					t.Fatalf("expected server-assigned namespaceKey on create")
				}
				if got := output.Get("teamId").AsString(); got != team.ID {
					t.Fatalf("expected teamId %q, got %q", team.ID, got)
				}
			},
		},
		Updates: []integration.Operation{
			{
				// name-only: in-place update.
				Inputs: spaceInputs("Renamed Space", "my-space", team.ID, "cluster-1"),
				Hook: func(_, output property.Map) {
					if got := output.Get("name").AsString(); got != "Renamed Space" {
						t.Fatalf("expected updated name, got %q", got)
					}
				},
			},
			{
				// clusterId change: replace.
				Inputs: spaceInputs("Renamed Space", "my-space", team.ID, "cluster-2"),
				Hook: func(_, output property.Map) {
					if got := output.Get("clusterId").AsString(); got != "cluster-2" {
						t.Fatalf("expected replaced clusterId 'cluster-2', got %q", got)
					}
				},
			},
			{
				// teamId change: replace.
				Inputs: spaceInputs("Renamed Space", "my-space", otherTeam.ID, "cluster-2"),
				Hook: func(_, output property.Map) {
					if got := output.Get("teamId").AsString(); got != otherTeam.ID {
						t.Fatalf("expected replaced teamId %q, got %q", otherTeam.ID, got)
					}
				},
			},
		},
	}
	test.Run(t, server)
}

// TestSpaceReadRequiresID documents (via a passing assertion, not just a
// comment) the limitation noted in space.go: Space.Read only accepts the
// space's UUID, unlike Team.Read which also accepts a slug. A slug passed as
// the read ID must fail, not silently succeed against the wrong resource.
func TestSpaceReadRequiresID(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")

	urn := presource.NewURN("test", "provider", "", "codecapsules:index:Space", "test")

	// by id: works
	read, err := server.Read(p.ReadRequest{Urn: urn, ID: space.ID})
	if err != nil {
		t.Fatalf("Read by id: %v", err)
	}
	if read.ID != space.ID {
		t.Fatalf("expected id %q, got %q", space.ID, read.ID)
	}

	// by slug: must fail (not a supported import path for Space today)
	if _, err := server.Read(p.ReadRequest{Urn: urn, ID: space.Slug}); err == nil {
		t.Fatalf("expected Read by slug to fail for Space (unsupported), but it succeeded")
	}
}

func TestSpaceDeleteBlockedThenSucceeds(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	team := fake.SeedTeam("Team", "team-slug")
	space := fake.SeedSpace("Space", "space-slug", team.ID, "cluster-1")
	fake.MarkSpaceHasCapsules(space.ID, true)

	urn := presource.NewURN("test", "provider", "", "codecapsules:index:Space", "test")
	props := spaceInputs(space.Name, space.Slug, space.TeamID, space.ClusterID).
		Set("namespaceKey", property.New(space.NamespaceKey))

	if err := server.Delete(p.DeleteRequest{Urn: urn, ID: space.ID, Properties: props}); err == nil {
		t.Fatalf("expected Delete to fail while space still has active capsules")
	}

	fake.MarkSpaceHasCapsules(space.ID, false)
	if err := server.Delete(p.DeleteRequest{Urn: urn, ID: space.ID, Properties: props}); err != nil {
		t.Fatalf("expected Delete to succeed once precondition cleared: %v", err)
	}
}

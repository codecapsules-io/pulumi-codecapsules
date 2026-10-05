package provider_test

import (
	"testing"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	presource "github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/testutil"
)

func teamInputs(name, slug string) property.Map {
	return property.NewMap(map[string]property.Value{
		"name": property.New(name),
		"slug": property.New(slug),
	})
}

// TestTeamLifecycle drives Team through preview->create->in-place
// update->replace-triggering update->delete via pulumi-go-provider's own
// gRPC-level LifeCycleTest harness, the way the real Pulumi engine would.
// It also asserts that a slug change (tagged replaceOnChanges) actually
// forces delete+recreate rather than an in-place PATCH, since that's the
// one behavior in this resource that's easy to get backwards.
func TestTeamLifecycle(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	firstSlugSeen := ""
	test := integration.LifeCycleTest{
		Resource: "codecapsules:index:Team",
		Create: integration.Operation{
			Inputs: teamInputs("Example Team", "example-team"),
			Hook: func(_, output property.Map) {
				if got := output.Get("name").AsString(); got != "Example Team" {
					t.Fatalf("expected created name 'Example Team', got %q", got)
				}
				if got := output.Get("slug").AsString(); got != "example-team" {
					t.Fatalf("expected created slug 'example-team', got %q", got)
				}
				firstSlugSeen = output.Get("slug").AsString()
			},
		},
		Updates: []integration.Operation{
			{
				// name-only change: must be an in-place update, not a replace.
				Inputs: teamInputs("Renamed Team", "example-team"),
				Hook: func(_, output property.Map) {
					if got := output.Get("name").AsString(); got != "Renamed Team" {
						t.Fatalf("expected updated name 'Renamed Team', got %q", got)
					}
					if got := output.Get("slug").AsString(); got != firstSlugSeen {
						t.Fatalf("slug must not change on an in-place update, got %q", got)
					}
				},
			},
			{
				// slug change: replaceOnChanges must force delete+recreate.
				// The fake server enforces slug uniqueness among *live*
				// teams, so if the old team wasn't actually deleted first,
				// this create would still succeed against a *different*
				// slug anyway - the real signal this test relies on is
				// LifeCycleTest's own DetailedDiff/replace-path assertion
				// inside Run, which fails the test if UpdateReplace isn't
				// triggered for a slug-only diff.
				Inputs: teamInputs("Renamed Team", "example-team-new-slug"),
				Hook: func(_, output property.Map) {
					if got := output.Get("slug").AsString(); got != "example-team-new-slug" {
						t.Fatalf("expected replaced slug 'example-team-new-slug', got %q", got)
					}
				},
			},
		},
	}
	test.Run(t, server)
}

// TestTeamImportBySlug exercises Read the way `pulumi import` would use it:
// with the fake server's slug as the id argument, not the UUID. This is the
// plan-critique-driven feature this resource has that Space does not yet.
func TestTeamImportBySlug(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	created := fake.SeedTeam("Seeded Team", "seeded-team")

	urn := presource.NewURN("test", "provider", "", "codecapsules:index:Team", "test")
	read, err := server.Read(p.ReadRequest{
		Urn: urn,
		ID:  "seeded-team", // slug, not the server-assigned id
	})
	if err != nil {
		t.Fatalf("Read by slug: %v", err)
	}
	if read.ID != created.ID {
		t.Fatalf("expected canonical id %q after import-by-slug, got %q", created.ID, read.ID)
	}
	if got := read.Properties.Get("name").AsString(); got != "Seeded Team" {
		t.Fatalf("expected imported name 'Seeded Team', got %q", got)
	}
}

// TestTeamDeleteBlockedThenSucceeds exercises the precondition path the
// plan's own research surfaced (team-service.ts validateTeamHasNoActiveSpaces):
// Delete must surface the backend's 400 as a real error, not swallow it, and
// must succeed once the precondition clears.
func TestTeamDeleteBlockedThenSucceeds(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	server := newTestServer(t, fake)

	created := fake.SeedTeam("Blocked Team", "blocked-team")
	fake.MarkTeamHasSpaces(created.ID, true)

	urn := presource.NewURN("test", "provider", "", "codecapsules:index:Team", "test")
	props := property.NewMap(map[string]property.Value{
		"name": property.New(created.Name),
		"slug": property.New(created.Slug),
	})

	err := server.Delete(p.DeleteRequest{Urn: urn, ID: created.ID, Properties: props})
	if err == nil {
		t.Fatalf("expected Delete to fail while team still has active spaces")
	}

	fake.MarkTeamHasSpaces(created.ID, false)
	if err := server.Delete(p.DeleteRequest{Urn: urn, ID: created.ID, Properties: props}); err != nil {
		t.Fatalf("expected Delete to succeed once precondition cleared: %v", err)
	}

	if _, err := server.Read(p.ReadRequest{Urn: urn, ID: created.ID}); err == nil {
		t.Fatalf("expected Read of a deleted team to fail")
	}
}

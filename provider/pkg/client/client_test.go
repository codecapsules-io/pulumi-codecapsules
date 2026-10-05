package client_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/testutil"
)

func newTestClient(t *testing.T, fake *testutil.FakeServer) *client.Client {
	t.Helper()
	return client.New(client.Config{
		ApiURL: fake.URL,
		ApiKey: "test-key",
	})
}

func TestCreateAndGetTeam(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	c := newTestClient(t, fake)

	team, err := c.CreateTeam(context.Background(), "Example Team", "example-team")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.ID == "" {
		t.Fatalf("expected server-assigned ID, got empty")
	}
	if team.Name != "Example Team" || team.Slug != "example-team" {
		t.Fatalf("unexpected team: %+v", team)
	}

	got, err := c.GetTeam(context.Background(), team.ID)
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if got.ID != team.ID {
		t.Fatalf("expected id %q, got %q", team.ID, got.ID)
	}

	// Read via slug too, since Read/import must support the slug form.
	bySlug, err := c.GetTeam(context.Background(), team.Slug)
	if err != nil {
		t.Fatalf("GetTeam by slug: %v", err)
	}
	if bySlug.ID != team.ID {
		t.Fatalf("expected id %q via slug lookup, got %q", team.ID, bySlug.ID)
	}
}

func TestGetTeamNotFound(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	c := newTestClient(t, fake)

	_, err := c.GetTeam(context.Background(), "does-not-exist")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || !apiErr.IsNotFound() {
		t.Fatalf("expected 404 APIError, got %v", err)
	}
}

func TestDeleteTeamBlockedByActiveSpaces(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	c := newTestClient(t, fake)

	team, err := c.CreateTeam(context.Background(), "T", "t-slug")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	fake.MarkTeamHasSpaces(team.ID, true)

	err = c.DeleteTeam(context.Background(), team.ID)
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || !apiErr.IsBadRequest() {
		t.Fatalf("expected 400 APIError for team-has-spaces, got %v", err)
	}
}

func TestUpdateTeamNameOnly(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	c := newTestClient(t, fake)

	team, err := c.CreateTeam(context.Background(), "Old Name", "fixed-slug")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	updated, err := c.UpdateTeam(context.Background(), team.ID, "New Name")
	if err != nil {
		t.Fatalf("UpdateTeam: %v", err)
	}
	if updated.Name != "New Name" {
		t.Fatalf("expected updated name, got %q", updated.Name)
	}
	if updated.Slug != "fixed-slug" {
		t.Fatalf("slug must be immutable via update, got %q", updated.Slug)
	}
}

func TestCreateSpaceRequiresValidTeam(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	c := newTestClient(t, fake)

	_, err := c.CreateSpace(context.Background(), "Space", "space-slug", "no-such-team", "cluster-1")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || !apiErr.IsBadRequest() {
		t.Fatalf("expected 400 APIError for invalid team, got %v", err)
	}
}

func TestSpaceLifecycle(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	c := newTestClient(t, fake)
	ctx := context.Background()

	team, err := c.CreateTeam(ctx, "Team", "team-slug")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	space, err := c.CreateSpace(ctx, "Space", "space-slug", team.ID, "cluster-1")
	if err != nil {
		t.Fatalf("CreateSpace: %v", err)
	}
	if space.NamespaceKey == "" {
		t.Fatalf("expected server-assigned namespaceKey")
	}

	// blocked delete
	fake.MarkSpaceHasCapsules(space.ID, true)
	err = c.DeleteSpace(ctx, space.ID)
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || !apiErr.IsBadRequest() {
		t.Fatalf("expected 400 for space-has-capsules, got %v", err)
	}

	// unblock and delete for real
	fake.MarkSpaceHasCapsules(space.ID, false)
	if err := c.DeleteSpace(ctx, space.ID); err != nil {
		t.Fatalf("DeleteSpace: %v", err)
	}

	// soft-deleted space reads as 404
	_, err = c.GetSpace(ctx, space.ID)
	if !errors.As(err, &apiErr) || !apiErr.IsNotFound() {
		t.Fatalf("expected 404 after delete, got %v", err)
	}
}

func TestUnauthorizedMappedAsAPIError(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	c := client.New(client.Config{ApiURL: fake.URL, ApiKey: ""})

	// FakeServer requires an Authorization header; our client always sends
	// one (possibly "Bearer "), so force the fake to reject explicitly by
	// requiring auth and stripping it isn't directly possible here - instead
	// verify the 401 path via fault injection, which is what matters: that
	// APIError.IsUnauthorized() works end-to-end through the client.
	fake.InjectFault(testutil.Fault{
		Method: http.MethodGet, PathPrefix: "/teams/", Status: http.StatusUnauthorized, Body: "unauthorized", Remaining: 1,
	})

	_, err := c.GetTeam(context.Background(), "anything")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || !apiErr.IsUnauthorized() {
		t.Fatalf("expected 401 APIError, got %v", err)
	}
}

func TestRetriesOnRateLimitThenSucceeds(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	c := newTestClient(t, fake)
	ctx := context.Background()

	team, err := c.CreateTeam(ctx, "T", "t-slug")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	// GET is idempotent, so the client should retry through a couple of 429s.
	fake.InjectFault(testutil.Fault{Method: http.MethodGet, PathPrefix: "/teams/", Status: http.StatusTooManyRequests, Body: "slow down", Remaining: 2})

	got, err := c.GetTeam(ctx, team.ID)
	if err != nil {
		t.Fatalf("expected GetTeam to succeed after retrying past 429s, got %v", err)
	}
	if got.ID != team.ID {
		t.Fatalf("unexpected team after retry: %+v", got)
	}
}

func TestPostNotRetriedOnRateLimitPastBudget(t *testing.T) {
	fake := testutil.New()
	defer fake.Close()
	// MaxRetries=1 so the fault (which never runs out) proves POST gives up
	// rather than looping forever - it should still surface as an APIError,
	// not an ambiguous transport error.
	c := client.New(client.Config{ApiURL: fake.URL, ApiKey: "test-key", MaxRetries: 1})

	fake.InjectFault(testutil.Fault{Method: http.MethodPost, PathPrefix: "/teams/", Status: http.StatusTooManyRequests, Body: "slow down", Remaining: 100})

	_, err := c.CreateTeam(context.Background(), "T", "t-slug")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || !apiErr.IsRateLimited() {
		t.Fatalf("expected 429 APIError after exhausting retry budget, got %v", err)
	}
}

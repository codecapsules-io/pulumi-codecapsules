package provider

import (
	"context"
	"errors"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
)

// Team manages a Code Capsules team via `api`'s /teams endpoints
// (api/src/api/team/team-routes.ts, api/src/services/team-service.ts).
type Team struct{}

type TeamArgs struct {
	Name string `pulumi:"name"`
	// Slug is immutable server-side: team-service.ts updateTeam only ever
	// writes team.name, never slug. See Diff below for why this is wired
	// through a hand-written Diff rather than a `provider:"replaceOnChanges"`
	// tag: the default tag-driven Diff never sets DeleteBeforeReplace
	// (infer/resource.go leaves it a literal `// TODO: how should we set
	// this?`), and slug is server-enforced-unique, so a create-before-delete
	// replace would always 409 against the not-yet-deleted old team.
	Slug string `pulumi:"slug"`
}

type TeamState struct {
	Name string `pulumi:"name"`
	Slug string `pulumi:"slug"`
}

func (*Team) Annotate(a infer.Annotator) {
	a.SetToken("index", "Team")
}

func (args *TeamArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.Name, "The team's display name.")
	a.Describe(&args.Slug, "The team's URL-safe slug. Immutable after creation; changing it replaces the resource.")
}

func (t *Team) Create(ctx context.Context, req infer.CreateRequest[TeamArgs]) (infer.CreateResponse[TeamState], error) {
	if req.DryRun {
		return infer.CreateResponse[TeamState]{
			ID:     "preview-" + req.Name,
			Output: TeamState{Name: req.Inputs.Name, Slug: req.Inputs.Slug},
		}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	team, err := c.CreateTeam(ctx, req.Inputs.Name, req.Inputs.Slug)
	if err != nil {
		return infer.CreateResponse[TeamState]{}, err
	}
	return infer.CreateResponse[TeamState]{
		ID:     team.ID,
		Output: TeamState{Name: team.Name, Slug: team.Slug},
	}, nil
}

// Read supports both `pulumi refresh` (req.ID is the team UUID) and
// `pulumi import` using the team's slug, since GetTeam accepts either -
// this is the provider's answer to the plan's "no import support" gap for
// the one resource (Team) simple enough not to need the two-hop
// Space->cluster resolution that Domain/Capsule/Database will require.
func (t *Team) Read(ctx context.Context, req infer.ReadRequest[TeamArgs, TeamState]) (infer.ReadResponse[TeamArgs, TeamState], error) {
	c := infer.GetConfig[Config](ctx).mustClient()
	team, err := c.GetTeam(ctx, req.ID)
	if err != nil {
		return infer.ReadResponse[TeamArgs, TeamState]{}, err
	}
	return infer.ReadResponse[TeamArgs, TeamState]{
		ID:     team.ID,
		Inputs: TeamArgs{Name: team.Name, Slug: team.Slug},
		State:  TeamState{Name: team.Name, Slug: team.Slug},
	}, nil
}

// Diff is hand-written rather than left to infer's tag-driven default -
// see the Slug field comment above for why.
func (t *Team) Diff(ctx context.Context, req infer.DiffRequest[TeamArgs, TeamState]) (infer.DiffResponse, error) {
	diff := map[string]p.PropertyDiff{}
	if req.Inputs.Name != req.State.Name {
		diff["name"] = p.PropertyDiff{Kind: p.Update}
	}
	if req.Inputs.Slug != req.State.Slug {
		diff["slug"] = p.PropertyDiff{Kind: p.UpdateReplace}
	}
	return infer.DiffResponse{
		DeleteBeforeReplace: true,
		HasChanges:          len(diff) > 0,
		DetailedDiff:        diff,
	}, nil
}

func (t *Team) Update(ctx context.Context, req infer.UpdateRequest[TeamArgs, TeamState]) (infer.UpdateResponse[TeamState], error) {
	if req.DryRun {
		return infer.UpdateResponse[TeamState]{
			Output: TeamState{Name: req.Inputs.Name, Slug: req.State.Slug},
		}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	team, err := c.UpdateTeam(ctx, req.ID, req.Inputs.Name)
	if err != nil {
		return infer.UpdateResponse[TeamState]{}, err
	}
	return infer.UpdateResponse[TeamState]{
		Output: TeamState{Name: team.Name, Slug: team.Slug},
	}, nil
}

func (t *Team) Delete(ctx context.Context, req infer.DeleteRequest[TeamState]) (infer.DeleteResponse, error) {
	c := infer.GetConfig[Config](ctx).mustClient()
	err := c.DeleteTeam(ctx, req.ID)
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		// Already gone - deleting a resource that doesn't exist is success,
		// not an error, for Pulumi's purposes.
		return infer.DeleteResponse{}, nil
	}
	return infer.DeleteResponse{}, err
}

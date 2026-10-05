package provider

import (
	"context"
	"errors"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
)

// Space manages a Code Capsules space via `api`'s /spaces endpoints
// (api/src/services/space-service.ts). A Space is scoped to a Team and a
// Cluster and owns a namespace on that cluster.
//
// Space is deliberately still an `api`-only, single-hop resource: it does
// not yet need the Space->cluster.clusterApiEndpoint two-hop resolution that
// Domain/Capsule/Database (capsule-api resources, out of scope for this
// implementation pass) will require - see the plan's "no resource ID/URN
// design" critique finding for why that resolution needs a decision before
// those resources are built.
type Space struct{}

type SpaceArgs struct {
	Name string `pulumi:"name"`
	// Slug, TeamID and ClusterID are immutable server-side: space-service.ts
	// updateSpace only ever writes existingSpace.name. Changing any of these
	// requires deleting and recreating the Space (and its namespace). See
	// Diff below for why this is a hand-written Diff rather than a
	// `provider:"replaceOnChanges"` tag (same reasoning as team.go's Slug
	// field: no DeleteBeforeReplace from the tag-driven default, and slug is
	// server-enforced-unique).
	Slug      string `pulumi:"slug"`
	TeamID    string `pulumi:"teamId"`
	ClusterID string `pulumi:"clusterId"`
}

type SpaceState struct {
	Name      string `pulumi:"name"`
	Slug      string `pulumi:"slug"`
	TeamID    string `pulumi:"teamId"`
	ClusterID string `pulumi:"clusterId"`
	// NamespaceKey is server-assigned during Create - see
	// space-service.ts createSpace, which synchronously provisions the
	// cluster namespace before returning. Never a Create input.
	NamespaceKey string `pulumi:"namespaceKey"`
}

func (*Space) Annotate(a infer.Annotator) {
	a.SetToken("index", "Space")
}

func (args *SpaceArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.Name, "The space's display name.")
	a.Describe(&args.Slug, "The space's URL-safe slug. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.TeamID, "The owning Team's id. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.ClusterID, "The cluster this space's namespace is provisioned on. Immutable after creation; changing it replaces the resource.")
}

func (s *Space) Create(ctx context.Context, req infer.CreateRequest[SpaceArgs]) (infer.CreateResponse[SpaceState], error) {
	if req.DryRun {
		return infer.CreateResponse[SpaceState]{
			ID: "preview-" + req.Name,
			Output: SpaceState{
				Name: req.Inputs.Name, Slug: req.Inputs.Slug,
				TeamID: req.Inputs.TeamID, ClusterID: req.Inputs.ClusterID,
			},
		}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	space, err := c.CreateSpace(ctx, req.Inputs.Name, req.Inputs.Slug, req.Inputs.TeamID, req.Inputs.ClusterID)
	if err != nil {
		return infer.CreateResponse[SpaceState]{}, err
	}
	return infer.CreateResponse[SpaceState]{
		ID:     space.ID,
		Output: spaceToState(space),
	}, nil
}

// Read supports `pulumi refresh`. Unlike Team, importing a Space by slug
// alone is not supported today because GetSpace only accepts the space's
// UUID (mirroring GET /spaces/{spaceId} - there is no GET /spaces/slug/{id}
// equivalent surfaced through this client yet); `pulumi import` therefore
// requires the space's id, not its slug.
func (s *Space) Read(ctx context.Context, req infer.ReadRequest[SpaceArgs, SpaceState]) (infer.ReadResponse[SpaceArgs, SpaceState], error) {
	c := infer.GetConfig[Config](ctx).mustClient()
	space, err := c.GetSpace(ctx, req.ID)
	if err != nil {
		return infer.ReadResponse[SpaceArgs, SpaceState]{}, err
	}
	return infer.ReadResponse[SpaceArgs, SpaceState]{
		ID: space.ID,
		Inputs: SpaceArgs{
			Name: space.Name, Slug: space.Slug,
			TeamID: space.TeamID, ClusterID: space.ClusterID,
		},
		State: spaceToState(space),
	}, nil
}

// Diff is hand-written rather than left to infer's tag-driven default -
// see the Slug/TeamID/ClusterID field comment above for why.
func (s *Space) Diff(ctx context.Context, req infer.DiffRequest[SpaceArgs, SpaceState]) (infer.DiffResponse, error) {
	diff := map[string]p.PropertyDiff{}
	if req.Inputs.Name != req.State.Name {
		diff["name"] = p.PropertyDiff{Kind: p.Update}
	}
	if req.Inputs.Slug != req.State.Slug {
		diff["slug"] = p.PropertyDiff{Kind: p.UpdateReplace}
	}
	if req.Inputs.TeamID != req.State.TeamID {
		diff["teamId"] = p.PropertyDiff{Kind: p.UpdateReplace}
	}
	if req.Inputs.ClusterID != req.State.ClusterID {
		diff["clusterId"] = p.PropertyDiff{Kind: p.UpdateReplace}
	}
	return infer.DiffResponse{
		DeleteBeforeReplace: true,
		HasChanges:          len(diff) > 0,
		DetailedDiff:        diff,
	}, nil
}

func (s *Space) Update(ctx context.Context, req infer.UpdateRequest[SpaceArgs, SpaceState]) (infer.UpdateResponse[SpaceState], error) {
	if req.DryRun {
		return infer.UpdateResponse[SpaceState]{
			Output: SpaceState{
				Name: req.Inputs.Name, Slug: req.State.Slug,
				TeamID: req.State.TeamID, ClusterID: req.State.ClusterID,
				NamespaceKey: req.State.NamespaceKey,
			},
		}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	space, err := c.UpdateSpaceName(ctx, req.ID, req.Inputs.Name)
	if err != nil {
		return infer.UpdateResponse[SpaceState]{}, err
	}
	return infer.UpdateResponse[SpaceState]{Output: spaceToState(space)}, nil
}

func (s *Space) Delete(ctx context.Context, req infer.DeleteRequest[SpaceState]) (infer.DeleteResponse, error) {
	c := infer.GetConfig[Config](ctx).mustClient()
	err := c.DeleteSpace(ctx, req.ID)
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		return infer.DeleteResponse{}, nil
	}
	return infer.DeleteResponse{}, err
}

func spaceToState(space *client.Space) SpaceState {
	return SpaceState{
		Name: space.Name, Slug: space.Slug,
		TeamID: space.TeamID, ClusterID: space.ClusterID,
		NamespaceKey: space.NamespaceKey,
	}
}

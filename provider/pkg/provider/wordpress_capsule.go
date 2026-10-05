package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
)

// WordpressCapsule manages a WordPress capsule. It links to its MySQL and
// storage capsules by id only - the platform resolves actual connection
// details server-side, so no host/port/credential copying is needed for
// those two. There is no equivalent Redis linking anywhere on the platform:
// if a consuming Pulumi program wants this capsule actually using a
// RedisCapsule, it must pass that Redis capsule's connection details
// through Env itself.
//
// Two deployment types are supported:
//
//   - "default": deploys a stock WordPress version/image. Create applies
//     the deployment immediately and the site is live once Create returns.
//   - "git": deploys a custom WordPress codebase from an already-connected
//     git repository. Create does **not** build or deploy anything itself -
//     it creates the capsule record and a push webhook, and the site only
//     goes live on the next actual `git push` to the given branch. This is
//     a real, load-bearing fact: a Pulumi `up` can succeed while the site
//     is still unbuilt. There is also no poll-to-ready here, unlike the
//     data capsule types - there is nothing to poll for until a push
//     happens.
//
// Create does not check that the referenced Mysql/StorageCapsule is Ready
// in either mode - safety against racing an unready database comes from
// MysqlCapsule/StorageCapsule's own poll-to-Ready Create, not from anything
// here; a Pulumi program's ordinary dependsOn/output-chaining on those
// resources' ids is what makes this safe.
type WordpressCapsule struct{}

type WordpressCapsuleArgs struct {
	SpaceID     string `pulumi:"spaceId"`
	Name        string `pulumi:"name"`
	Description string `pulumi:"description,optional"`

	// Version is required when DeploymentType is "default"; unused for "git".
	Version string `pulumi:"version,optional"`
	// DeploymentType defaults to "default" if omitted. Valid values:
	// "default" (stock version/image) or "git" (custom codebase from an
	// already-connected repository).
	DeploymentType string `pulumi:"deploymentType,optional"`

	// GitRepositoryID/Branch are required when DeploymentType is "git";
	// unused for "default". GitRepositoryID references a git repository
	// that must already be connected to Code Capsules (via the dashboard's
	// GitHub App install flow) - this resource cannot create that
	// connection, only reference it by id.
	GitRepositoryID string `pulumi:"gitRepositoryId,optional"`
	Branch          string `pulumi:"branch,optional"`
	// SourceSubpath is optional in both deployment modes, for repositories
	// where the WordPress root isn't at the repository root.
	SourceSubpath string `pulumi:"sourceSubpath,optional"`

	MysqlCapsuleID   string `pulumi:"mysqlCapsuleId"`
	DatabaseName     string `pulumi:"databaseName"`
	StorageCapsuleID string `pulumi:"storageCapsuleId"`

	// Env is applied as the capsule's full EnvConfig - a complete replace
	// on every change, not a merge. Marked secret wholesale (rather than
	// per-key) since this is the mechanism a consuming Pulumi program uses
	// to inject a RedisCapsule's connection string, which must always be
	// treated as sensitive.
	Env map[string]string `pulumi:"env,optional" provider:"secret"`

	CpuQty      float64 `pulumi:"cpuQty,optional"`
	CpuUnit     string  `pulumi:"cpuUnit,optional"`
	MemoryQty   float64 `pulumi:"memoryQty,optional"`
	MemoryUnit  string  `pulumi:"memoryUnit,optional"`
	StorageQty  float64 `pulumi:"storageQty,optional"`
	StorageUnit string  `pulumi:"storageUnit,optional"`
	Replicas    int     `pulumi:"replicas,optional"`
}

type WordpressCapsuleState struct {
	SpaceID          string `pulumi:"spaceId"`
	Name             string `pulumi:"name"`
	Description      string `pulumi:"description"`
	Version          string `pulumi:"version,optional"`
	DeploymentType   string `pulumi:"deploymentType"`
	GitRepositoryID  string `pulumi:"gitRepositoryId,optional"`
	Branch           string `pulumi:"branch,optional"`
	SourceSubpath    string `pulumi:"sourceSubpath,optional"`
	MysqlCapsuleID   string `pulumi:"mysqlCapsuleId"`
	DatabaseName     string `pulumi:"databaseName"`
	StorageCapsuleID string `pulumi:"storageCapsuleId"`
	// optional: an empty/nil map serializes as an absent property, and
	// without ,optional here infer's decode treats that as a missing
	// required field on the next Diff/Update - caught by
	// TestWordpressCapsuleLifecycle's env-update step.
	Env         map[string]string `pulumi:"env,optional" provider:"secret"`
	CpuQty      float64           `pulumi:"cpuQty"`
	CpuUnit     string            `pulumi:"cpuUnit"`
	MemoryQty   float64           `pulumi:"memoryQty"`
	MemoryUnit  string            `pulumi:"memoryUnit"`
	StorageQty  float64           `pulumi:"storageQty"`
	StorageUnit string            `pulumi:"storageUnit"`
	Replicas    int               `pulumi:"replicas"`

	// Hostname is server-assigned on create - never a Create input. Set for
	// both deployment types, independent of whether a "git"-mode capsule
	// has actually received its first push yet.
	Hostname string `pulumi:"hostname"`
}

func (*WordpressCapsule) Annotate(a infer.Annotator) {
	a.SetToken("index", "WordpressCapsule")
}

func (args *WordpressCapsuleArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.SpaceID, "The owning Space's id. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.Name, "The capsule's name. Immutable after creation; changing it replaces the resource (renaming isn't supported).")
	a.Describe(&args.Description, "The capsule's description. Mutable in place.")
	a.Describe(&args.Version, "The WordPress version to deploy. Required when deploymentType is \"default\", unused for \"git\". Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.DeploymentType, "Deployment type: \"default\" (stock WordPress version/image) or \"git\" (custom codebase from an already-connected repository). Defaults to \"default\". Immutable after creation.")
	a.Describe(&args.GitRepositoryID, "The id of a git repository already connected to Code Capsules (connect it via the dashboard first - this resource can only reference an existing connection, not create one). Required when deploymentType is \"git\", unused for \"default\". Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.Branch, "The branch to deploy from. Required when deploymentType is \"git\", unused for \"default\". Immutable after creation; changing it replaces the resource. Note: creating a \"git\" capsule does not deploy anything by itself - the site goes live on the next push to this branch.")
	a.Describe(&args.SourceSubpath, "Subpath within the repository where the WordPress root lives, for repositories that aren't WordPress at their root. Optional in either deployment mode. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.MysqlCapsuleID, "The id of an existing MysqlCapsule (must already be Ready) this site's database. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.DatabaseName, "The database name to use on the referenced MysqlCapsule. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.StorageCapsuleID, "The id of an existing StorageCapsule for this site's uploads/media. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.Env, "Environment variables for the capsule. Replaces the full set on every change - useful for wiring in a RedisCapsule's connection details.")
	a.Describe(&args.CpuQty, "CPU request quantity.")
	a.Describe(&args.CpuUnit, "CPU request unit, e.g. \"m\". Defaults to \"m\".")
	a.Describe(&args.MemoryQty, "Memory request quantity.")
	a.Describe(&args.MemoryUnit, "Memory request unit, e.g. \"M\". Defaults to \"M\".")
	a.Describe(&args.StorageQty, "Storage request quantity.")
	a.Describe(&args.StorageUnit, "Storage request unit, e.g. \"Gi\". Defaults to \"Gi\".")
	a.Describe(&args.Replicas, "Replica count. Defaults to 1.")
}

// validateWordpressDeployment resolves the effective deploymentType
// (defaulting empty to "default") and enforces the two modes' mutually
// exclusive required fields, matching the real backend's validation:
// "default" requires version; "git" requires gitRepositoryId and branch.
func validateWordpressDeployment(args WordpressCapsuleArgs) (string, error) {
	deploymentType := args.DeploymentType
	if deploymentType == "" {
		deploymentType = "default"
	}
	switch deploymentType {
	case "default":
		if args.Version == "" {
			return "", errors.New("version is required when deploymentType is \"default\"")
		}
		if args.GitRepositoryID != "" || args.Branch != "" {
			return "", errors.New("gitRepositoryId/branch are not used when deploymentType is \"default\" - did you mean to set deploymentType to \"git\"?")
		}
	case "git":
		if args.GitRepositoryID == "" || args.Branch == "" {
			return "", errors.New("gitRepositoryId and branch are both required when deploymentType is \"git\"")
		}
		if args.Version != "" {
			return "", errors.New("version is not used when deploymentType is \"git\" - did you mean to set deploymentType to \"default\"?")
		}
	default:
		return "", fmt.Errorf("deploymentType %q is not supported - must be \"default\" or \"git\"", deploymentType)
	}
	return deploymentType, nil
}

func (w *WordpressCapsule) Create(ctx context.Context, req infer.CreateRequest[WordpressCapsuleArgs]) (infer.CreateResponse[WordpressCapsuleState], error) {
	deploymentType, err := validateWordpressDeployment(req.Inputs)
	if err != nil {
		return infer.CreateResponse[WordpressCapsuleState]{}, err
	}

	if req.DryRun {
		return infer.CreateResponse[WordpressCapsuleState]{
			ID:     "preview-" + req.Name,
			Output: wordpressArgsToState(req.Inputs, deploymentType, ""),
		}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, req.Inputs.SpaceID)
	if err != nil {
		return infer.CreateResponse[WordpressCapsuleState]{}, err
	}

	manifest := map[string]interface{}{
		"manifestType":     "wordpress",
		"deploymentType":   deploymentType,
		"storageCapsuleId": req.Inputs.StorageCapsuleID,
		"database": map[string]interface{}{
			"mysqlCapsuleId": req.Inputs.MysqlCapsuleID,
			"databaseName":   req.Inputs.DatabaseName,
		},
	}
	if req.Inputs.SourceSubpath != "" {
		manifest["sourceSubpath"] = req.Inputs.SourceSubpath
	}
	switch deploymentType {
	case "default":
		manifest["version"] = req.Inputs.Version
	case "git":
		manifest["gitRepositoryId"] = req.Inputs.GitRepositoryID
		manifest["branch"] = req.Inputs.Branch
	}
	if len(req.Inputs.Env) > 0 {
		manifest["configs"] = envToConfigs(req.Inputs.Env)
	}
	products := productsFrom(req.Inputs.CpuQty, req.Inputs.MemoryQty, req.Inputs.StorageQty,
		req.Inputs.CpuUnit, req.Inputs.MemoryUnit, req.Inputs.StorageUnit, req.Inputs.Replicas)

	capsule, err := c.CreateCapsule(ctx, endpoint, namespaceKey, req.Inputs.Name, req.Inputs.Description, manifest, products)
	if err != nil {
		return infer.CreateResponse[WordpressCapsuleState]{}, err
	}

	return infer.CreateResponse[WordpressCapsuleState]{
		ID:     capsule.ID,
		Output: wordpressArgsToState(req.Inputs, deploymentType, capsule.PublicAccessHostname()),
	}, nil
}

// Read supports `pulumi refresh` only - see MysqlCapsule.Read's comment.
func (w *WordpressCapsule) Read(ctx context.Context, req infer.ReadRequest[WordpressCapsuleArgs, WordpressCapsuleState]) (infer.ReadResponse[WordpressCapsuleArgs, WordpressCapsuleState], error) {
	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, _, err := resolveSpaceEndpoint(ctx, c, req.State.SpaceID)
	if err != nil {
		return infer.ReadResponse[WordpressCapsuleArgs, WordpressCapsuleState]{}, err
	}
	capsule, err := c.GetCapsule(ctx, endpoint, req.ID)
	if err != nil {
		return infer.ReadResponse[WordpressCapsuleArgs, WordpressCapsuleState]{}, err
	}
	deploymentType := req.Inputs.DeploymentType
	if deploymentType == "" {
		deploymentType = "default"
	}
	return infer.ReadResponse[WordpressCapsuleArgs, WordpressCapsuleState]{
		ID:     capsule.ID,
		Inputs: req.Inputs,
		State:  wordpressArgsToState(req.Inputs, deploymentType, capsule.PublicAccessHostname()),
	}, nil
}

// Diff treats everything except description/env/sizing as immutable
// (replace-triggering) - deliberately conservative, matching the base
// plan's critique recommendation to default stateful/linking fields to
// replace-by-default until proven safe to update in place, rather than
// guessing that capsule-api's PATCH .../capsule/{id}/manifest endpoint
// safely redeploys a version/database-link/git-source change (not verified
// this pass).
func (w *WordpressCapsule) Diff(ctx context.Context, req infer.DiffRequest[WordpressCapsuleArgs, WordpressCapsuleState]) (infer.DiffResponse, error) {
	diff := map[string]p.PropertyDiff{}
	replaceIfChanged := map[string]struct{ new, old string }{
		"spaceId":          {req.Inputs.SpaceID, req.State.SpaceID},
		"name":             {req.Inputs.Name, req.State.Name},
		"version":          {req.Inputs.Version, req.State.Version},
		"gitRepositoryId":  {req.Inputs.GitRepositoryID, req.State.GitRepositoryID},
		"branch":           {req.Inputs.Branch, req.State.Branch},
		"sourceSubpath":    {req.Inputs.SourceSubpath, req.State.SourceSubpath},
		"mysqlCapsuleId":   {req.Inputs.MysqlCapsuleID, req.State.MysqlCapsuleID},
		"databaseName":     {req.Inputs.DatabaseName, req.State.DatabaseName},
		"storageCapsuleId": {req.Inputs.StorageCapsuleID, req.State.StorageCapsuleID},
	}
	deploymentType := req.Inputs.DeploymentType
	if deploymentType == "" {
		deploymentType = "default"
	}
	if deploymentType != req.State.DeploymentType {
		diff["deploymentType"] = p.PropertyDiff{Kind: p.UpdateReplace}
	}
	for field, v := range replaceIfChanged {
		if v.new != v.old {
			diff[field] = p.PropertyDiff{Kind: p.UpdateReplace}
		}
	}
	if req.Inputs.Description != req.State.Description {
		diff["description"] = p.PropertyDiff{Kind: p.Update}
	}
	if !reflect.DeepEqual(req.Inputs.Env, req.State.Env) {
		diff["env"] = p.PropertyDiff{Kind: p.Update}
	}
	newProducts := productsFrom(req.Inputs.CpuQty, req.Inputs.MemoryQty, req.Inputs.StorageQty,
		req.Inputs.CpuUnit, req.Inputs.MemoryUnit, req.Inputs.StorageUnit, req.Inputs.Replicas)
	oldProducts := productsFrom(req.State.CpuQty, req.State.MemoryQty, req.State.StorageQty,
		req.State.CpuUnit, req.State.MemoryUnit, req.State.StorageUnit, req.State.Replicas)
	if newProducts != oldProducts {
		diff["products"] = p.PropertyDiff{Kind: p.Update}
	}
	return infer.DiffResponse{
		DeleteBeforeReplace: true,
		HasChanges:          len(diff) > 0,
		DetailedDiff:        diff,
	}, nil
}

func (w *WordpressCapsule) Update(ctx context.Context, req infer.UpdateRequest[WordpressCapsuleArgs, WordpressCapsuleState]) (infer.UpdateResponse[WordpressCapsuleState], error) {
	deploymentType := req.Inputs.DeploymentType
	if deploymentType == "" {
		deploymentType = "default"
	}

	if req.DryRun {
		return infer.UpdateResponse[WordpressCapsuleState]{
			Output: wordpressArgsToState(req.Inputs, deploymentType, req.State.Hostname),
		}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, req.State.SpaceID)
	if err != nil {
		return infer.UpdateResponse[WordpressCapsuleState]{}, err
	}

	if _, err := c.UpdateCapsuleDescription(ctx, endpoint, namespaceKey, req.ID, req.Inputs.Description); err != nil {
		return infer.UpdateResponse[WordpressCapsuleState]{}, err
	}
	if !reflect.DeepEqual(req.Inputs.Env, req.State.Env) {
		if err := c.SetCapsuleConfigs(ctx, endpoint, req.ID, envToConfigs(req.Inputs.Env)); err != nil {
			return infer.UpdateResponse[WordpressCapsuleState]{}, err
		}
	}
	products := productsFrom(req.Inputs.CpuQty, req.Inputs.MemoryQty, req.Inputs.StorageQty,
		req.Inputs.CpuUnit, req.Inputs.MemoryUnit, req.Inputs.StorageUnit, req.Inputs.Replicas)
	if _, err := c.UpdateCapsuleProducts(ctx, endpoint, req.ID, products); err != nil {
		return infer.UpdateResponse[WordpressCapsuleState]{}, err
	}

	return infer.UpdateResponse[WordpressCapsuleState]{
		Output: wordpressArgsToState(req.Inputs, deploymentType, req.State.Hostname),
	}, nil
}

func (w *WordpressCapsule) Delete(ctx context.Context, req infer.DeleteRequest[WordpressCapsuleState]) (infer.DeleteResponse, error) {
	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, req.State.SpaceID)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.DeleteCapsule(ctx, endpoint, namespaceKey, req.ID)
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		return infer.DeleteResponse{}, nil
	}
	return infer.DeleteResponse{}, err
}

func wordpressArgsToState(args WordpressCapsuleArgs, deploymentType, hostname string) WordpressCapsuleState {
	return WordpressCapsuleState{
		SpaceID: args.SpaceID, Name: args.Name, Description: args.Description,
		Version: args.Version, DeploymentType: deploymentType,
		GitRepositoryID: args.GitRepositoryID, Branch: args.Branch, SourceSubpath: args.SourceSubpath,
		MysqlCapsuleID: args.MysqlCapsuleID, DatabaseName: args.DatabaseName,
		StorageCapsuleID: args.StorageCapsuleID, Env: args.Env,
		CpuQty: args.CpuQty, CpuUnit: args.CpuUnit,
		MemoryQty: args.MemoryQty, MemoryUnit: args.MemoryUnit,
		StorageQty: args.StorageQty, StorageUnit: args.StorageUnit,
		Replicas: args.Replicas,
		Hostname: hostname,
	}
}

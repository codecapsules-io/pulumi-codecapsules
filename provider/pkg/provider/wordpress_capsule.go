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

// WordpressCapsule manages a WordPress capsule - capsule-api's
// `manifestType: "wordpress"` Capsule
// (capsule-api/src/capsule/cargo-types/wordpress-cargo/wordpress-cargo.ts).
// It links to its MySQL and storage capsules by id only
// (WordpressManifestRequest.storageCapsuleId / database.mysqlCapsuleId -
// capsule-api/.../wordpress-cargo/models/wordpress-manifest-request.dto.ts) -
// the platform resolves actual connection details server-side, so no
// host/port/credential copying is needed for those two. There is no
// equivalent `redisCapsuleId` field anywhere on the platform: if a consuming
// Pulumi program wants this capsule actually using a RedisCapsule, it must
// pass that Redis capsule's connection details through Env itself.
//
// Create does not poll: verified directly against wordpress-cargo.ts's
// create(), which calls deploymentService.applyDeployment and returns with
// no async status machinery (unlike the data capsule types). It also does
// NOT check that the referenced Mysql/StorageCapsule is Ready
// (isManifestValid only checks the ids are present) - safety against
// racing an unready database comes from MysqlCapsule/StorageCapsule's own
// poll-to-Ready Create, not from anything here; a Pulumi program's ordinary
// dependsOn/output-chaining on those resources' ids is what makes this safe.
//
// Only `deploymentType: "default"` (image/version-based) is supported in
// this pass - the `git` deployment path
// (WordpressDeploymentType.git, requiring a `repo` block) is explicitly out
// of scope; Create returns a clear error if deploymentType is set to
// anything else rather than silently misbehaving.
type WordpressCapsule struct{}

type WordpressCapsuleArgs struct {
	SpaceID     string `pulumi:"spaceId"`
	Name        string `pulumi:"name"`
	Description string `pulumi:"description,optional"`

	// Version is required when DeploymentType is "default" (the only
	// supported value) - mirrors wordpress-cargo.ts's isManifestValid.
	Version string `pulumi:"version,optional"`
	// DeploymentType defaults to "default" if omitted. "git" is rejected -
	// see the type doc comment.
	DeploymentType string `pulumi:"deploymentType,optional"`

	MysqlCapsuleID   string `pulumi:"mysqlCapsuleId"`
	DatabaseName     string `pulumi:"databaseName"`
	StorageCapsuleID string `pulumi:"storageCapsuleId"`

	// Env is applied as the capsule's full EnvConfig
	// (PUT /capsules/{id}/configs, capsule-api/src/config/controller/
	// set-configs.controller.ts) - a complete replace on every Update, not a
	// merge. Marked secret wholesale (rather than per-key) since this is the
	// mechanism a consuming Pulumi program uses to inject a RedisCapsule's
	// connection string, which must always be treated as sensitive.
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
	Version          string `pulumi:"version"`
	DeploymentType   string `pulumi:"deploymentType"`
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

	// Hostname is server-assigned on create
	// (wordpress-manifest-request.dto.ts's mapToCapsule sets
	// jsonManifest.publicAccessHostname) - never a Create input.
	Hostname string `pulumi:"hostname"`
}

func (*WordpressCapsule) Annotate(a infer.Annotator) {
	a.SetToken("index", "WordpressCapsule")
}

func (args *WordpressCapsuleArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.SpaceID, "The owning Space's id. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.Name, "The capsule's name. Immutable after creation; changing it replaces the resource (renaming isn't supported).")
	a.Describe(&args.Description, "The capsule's description. Mutable in place.")
	a.Describe(&args.Version, "The WordPress version to deploy. Required when deploymentType is \"default\". Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.DeploymentType, "Deployment type. Only \"default\" is supported; defaults to \"default\". Immutable after creation.")
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

func (w *WordpressCapsule) Create(ctx context.Context, req infer.CreateRequest[WordpressCapsuleArgs]) (infer.CreateResponse[WordpressCapsuleState], error) {
	deploymentType := req.Inputs.DeploymentType
	if deploymentType == "" {
		deploymentType = "default"
	}
	if deploymentType != "default" {
		return infer.CreateResponse[WordpressCapsuleState]{}, fmt.Errorf(
			"deploymentType %q is not supported by this resource - only \"default\" (image/version-based) is implemented, not \"git\"", deploymentType)
	}
	if req.Inputs.Version == "" {
		return infer.CreateResponse[WordpressCapsuleState]{}, errors.New("version is required when deploymentType is \"default\"")
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
		"version":          req.Inputs.Version,
		"storageCapsuleId": req.Inputs.StorageCapsuleID,
		"database": map[string]interface{}{
			"mysqlCapsuleId": req.Inputs.MysqlCapsuleID,
			"databaseName":   req.Inputs.DatabaseName,
		},
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
// safely redeploys a version/database-link change (not verified this pass).
func (w *WordpressCapsule) Diff(ctx context.Context, req infer.DiffRequest[WordpressCapsuleArgs, WordpressCapsuleState]) (infer.DiffResponse, error) {
	diff := map[string]p.PropertyDiff{}
	replaceIfChanged := map[string]struct{ new, old string }{
		"spaceId":          {req.Inputs.SpaceID, req.State.SpaceID},
		"name":             {req.Inputs.Name, req.State.Name},
		"version":          {req.Inputs.Version, req.State.Version},
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
		MysqlCapsuleID: args.MysqlCapsuleID, DatabaseName: args.DatabaseName,
		StorageCapsuleID: args.StorageCapsuleID, Env: args.Env,
		CpuQty: args.CpuQty, CpuUnit: args.CpuUnit,
		MemoryQty: args.MemoryQty, MemoryUnit: args.MemoryUnit,
		StorageQty: args.StorageQty, StorageUnit: args.StorageUnit,
		Replicas: args.Replicas,
		Hostname: hostname,
	}
}

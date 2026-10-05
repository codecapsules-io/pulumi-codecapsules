package provider

import (
	"context"
	"errors"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
)

// RedisCapsule manages a Redis data capsule - capsule-api's
// `manifestType: "data", dataType: "Redis"` Capsule. Same create/poll shape
// as MysqlCapsule; see mysql_capsule.go's doc comments for the shared
// reasoning (manifestType dispatch, poll-to-Ready requirement, no Failed
// status). There is deliberately no native WordPress<->Redis wiring at the
// platform level (no `redisCapsuleId` field exists anywhere on
// WordpressManifestRequest) - a consuming Pulumi program that wants
// WordPress actually using this Redis capsule must inject
// PrivateHostname/PrivatePort/PrivateConnectionString into
// WordpressCapsule's `env` itself.
type RedisCapsule struct{}

type RedisCapsuleArgs struct {
	SpaceID     string `pulumi:"spaceId"`
	Name        string `pulumi:"name"`
	Description string `pulumi:"description,optional"`

	CpuQty      float64 `pulumi:"cpuQty,optional"`
	CpuUnit     string  `pulumi:"cpuUnit,optional"`
	MemoryQty   float64 `pulumi:"memoryQty,optional"`
	MemoryUnit  string  `pulumi:"memoryUnit,optional"`
	StorageQty  float64 `pulumi:"storageQty,optional"`
	StorageUnit string  `pulumi:"storageUnit,optional"`
	Replicas    int     `pulumi:"replicas,optional"`
}

type RedisCapsuleState struct {
	SpaceID     string  `pulumi:"spaceId"`
	Name        string  `pulumi:"name"`
	Description string  `pulumi:"description"`
	CpuQty      float64 `pulumi:"cpuQty"`
	CpuUnit     string  `pulumi:"cpuUnit"`
	MemoryQty   float64 `pulumi:"memoryQty"`
	MemoryUnit  string  `pulumi:"memoryUnit"`
	StorageQty  float64 `pulumi:"storageQty"`
	StorageUnit string  `pulumi:"storageUnit"`
	Replicas    int     `pulumi:"replicas"`

	// Server-assigned on create (db-providers/redis/redis.ts's create()) -
	// never Create inputs.
	PrivateHostname         string `pulumi:"privateHostname"`
	PrivatePort             string `pulumi:"privatePort"`
	PrivateConnectionString string `pulumi:"privateConnectionString" provider:"secret"`
}

func (*RedisCapsule) Annotate(a infer.Annotator) {
	a.SetToken("index", "RedisCapsule")
}

func (args *RedisCapsuleArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.SpaceID, "The owning Space's id. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.Name, "The capsule's name. Immutable after creation; changing it replaces the resource (renaming isn't supported).")
	a.Describe(&args.Description, "The capsule's description. Mutable in place.")
	a.Describe(&args.CpuQty, "CPU request quantity.")
	a.Describe(&args.CpuUnit, "CPU request unit, e.g. \"m\". Defaults to \"m\".")
	a.Describe(&args.MemoryQty, "Memory request quantity.")
	a.Describe(&args.MemoryUnit, "Memory request unit, e.g. \"M\". Defaults to \"M\".")
	a.Describe(&args.StorageQty, "Storage request quantity.")
	a.Describe(&args.StorageUnit, "Storage request unit, e.g. \"Gi\". Defaults to \"Gi\".")
	a.Describe(&args.Replicas, "Replica count. Defaults to 1.")
}

func (r *RedisCapsule) Create(ctx context.Context, req infer.CreateRequest[RedisCapsuleArgs]) (infer.CreateResponse[RedisCapsuleState], error) {
	if req.DryRun {
		return infer.CreateResponse[RedisCapsuleState]{
			ID:     "preview-" + req.Name,
			Output: redisArgsToState(req.Inputs, RedisCapsuleState{}),
		}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, req.Inputs.SpaceID)
	if err != nil {
		return infer.CreateResponse[RedisCapsuleState]{}, err
	}

	manifest := map[string]interface{}{
		"manifestType": "data",
		"dataType":     "Redis",
		"name":         req.Inputs.Name,
	}
	products := productsFrom(req.Inputs.CpuQty, req.Inputs.MemoryQty, req.Inputs.StorageQty,
		req.Inputs.CpuUnit, req.Inputs.MemoryUnit, req.Inputs.StorageUnit, req.Inputs.Replicas)

	capsule, err := c.CreateCapsule(ctx, endpoint, namespaceKey, req.Inputs.Name, req.Inputs.Description, manifest, products)
	if err != nil {
		return infer.CreateResponse[RedisCapsuleState]{}, err
	}

	ready, err := pollDataCapsuleReady(ctx, c, endpoint, capsule.ID)
	if err != nil {
		return infer.CreateResponse[RedisCapsuleState]{}, err
	}

	return infer.CreateResponse[RedisCapsuleState]{
		ID:     ready.ID,
		Output: redisCapsuleToState(req.Inputs, ready),
	}, nil
}

// Read supports `pulumi refresh` only - see MysqlCapsule.Read's comment.
func (r *RedisCapsule) Read(ctx context.Context, req infer.ReadRequest[RedisCapsuleArgs, RedisCapsuleState]) (infer.ReadResponse[RedisCapsuleArgs, RedisCapsuleState], error) {
	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, _, err := resolveSpaceEndpoint(ctx, c, req.State.SpaceID)
	if err != nil {
		return infer.ReadResponse[RedisCapsuleArgs, RedisCapsuleState]{}, err
	}
	capsule, err := c.GetCapsule(ctx, endpoint, req.ID)
	if err != nil {
		return infer.ReadResponse[RedisCapsuleArgs, RedisCapsuleState]{}, err
	}
	return infer.ReadResponse[RedisCapsuleArgs, RedisCapsuleState]{
		ID:     capsule.ID,
		Inputs: req.Inputs,
		State:  redisCapsuleToState(req.Inputs, capsule),
	}, nil
}

func (r *RedisCapsule) Diff(ctx context.Context, req infer.DiffRequest[RedisCapsuleArgs, RedisCapsuleState]) (infer.DiffResponse, error) {
	diff := map[string]p.PropertyDiff{}
	if req.Inputs.SpaceID != req.State.SpaceID {
		diff["spaceId"] = p.PropertyDiff{Kind: p.UpdateReplace}
	}
	if req.Inputs.Name != req.State.Name {
		diff["name"] = p.PropertyDiff{Kind: p.UpdateReplace}
	}
	if req.Inputs.Description != req.State.Description {
		diff["description"] = p.PropertyDiff{Kind: p.Update}
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

func (r *RedisCapsule) Update(ctx context.Context, req infer.UpdateRequest[RedisCapsuleArgs, RedisCapsuleState]) (infer.UpdateResponse[RedisCapsuleState], error) {
	if req.DryRun {
		return infer.UpdateResponse[RedisCapsuleState]{Output: redisArgsToState(req.Inputs, req.State)}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, req.State.SpaceID)
	if err != nil {
		return infer.UpdateResponse[RedisCapsuleState]{}, err
	}

	if _, err := c.UpdateCapsuleDescription(ctx, endpoint, namespaceKey, req.ID, req.Inputs.Description); err != nil {
		return infer.UpdateResponse[RedisCapsuleState]{}, err
	}
	products := productsFrom(req.Inputs.CpuQty, req.Inputs.MemoryQty, req.Inputs.StorageQty,
		req.Inputs.CpuUnit, req.Inputs.MemoryUnit, req.Inputs.StorageUnit, req.Inputs.Replicas)
	capsule, err := c.UpdateCapsuleProducts(ctx, endpoint, req.ID, products)
	if err != nil {
		return infer.UpdateResponse[RedisCapsuleState]{}, err
	}

	return infer.UpdateResponse[RedisCapsuleState]{Output: redisCapsuleToState(req.Inputs, capsule)}, nil
}

func (r *RedisCapsule) Delete(ctx context.Context, req infer.DeleteRequest[RedisCapsuleState]) (infer.DeleteResponse, error) {
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

func redisArgsToState(args RedisCapsuleArgs, prior RedisCapsuleState) RedisCapsuleState {
	return RedisCapsuleState{
		SpaceID: args.SpaceID, Name: args.Name, Description: args.Description,
		CpuQty: args.CpuQty, CpuUnit: args.CpuUnit,
		MemoryQty: args.MemoryQty, MemoryUnit: args.MemoryUnit,
		StorageQty: args.StorageQty, StorageUnit: args.StorageUnit,
		Replicas:                args.Replicas,
		PrivateHostname:         prior.PrivateHostname,
		PrivatePort:             prior.PrivatePort,
		PrivateConnectionString: prior.PrivateConnectionString,
	}
}

func redisCapsuleToState(args RedisCapsuleArgs, capsule *client.Capsule) RedisCapsuleState {
	s := redisArgsToState(args, RedisCapsuleState{})
	s.PrivateHostname = capsule.PrivateHostname()
	s.PrivatePort = capsule.PrivatePort()
	s.PrivateConnectionString = capsule.PrivateConnectionString()
	return s
}

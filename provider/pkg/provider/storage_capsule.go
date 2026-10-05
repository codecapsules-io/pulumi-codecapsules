package provider

import (
	"context"
	"errors"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
)

// StorageCapsule manages a persistent-storage data capsule - capsule-api's
// `manifestType: "data", dataType: "PersistentStorage"` Capsule (not
// "PersistentStorageGanesha", which a separate, newer provider handles -
// PersistentStorage is the plain dataType value
// cargo-types/data-cargo/db-providers/rook-nfs/persistent-storage.ts
// registers under, and is what this pass targets since it's what the
// user's existing structure uses).
//
// Unlike Mysql/RedisCapsule, this does NOT poll to Ready: verified directly
// against persistent-storage.ts's create(), which sets
// `capsule.jsonManifest.status = 'Ready'` synchronously before returning,
// with no async provisioning step - there is nothing to wait for.
type StorageCapsule struct{}

type StorageCapsuleArgs struct {
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

type StorageCapsuleState struct {
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
}

func (*StorageCapsule) Annotate(a infer.Annotator) {
	a.SetToken("index", "StorageCapsule")
}

func (args *StorageCapsuleArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.SpaceID, "The owning Space's id. Immutable after creation; changing it replaces the resource.")
	a.Describe(&args.Name, "The capsule's name. Immutable after creation; changing it replaces the resource (renaming isn't supported).")
	a.Describe(&args.Description, "The capsule's description. Mutable in place.")
	a.Describe(&args.CpuQty, "CPU request quantity.")
	a.Describe(&args.CpuUnit, "CPU request unit, e.g. \"m\". Defaults to \"m\".")
	a.Describe(&args.MemoryQty, "Memory request quantity.")
	a.Describe(&args.MemoryUnit, "Memory request unit, e.g. \"M\". Defaults to \"M\".")
	a.Describe(&args.StorageQty, "Storage size requested. This is the primary sizing knob for a storage capsule.")
	a.Describe(&args.StorageUnit, "Storage size unit, e.g. \"Gi\". Defaults to \"Gi\".")
	a.Describe(&args.Replicas, "Replica count. Defaults to 1.")
}

func (s *StorageCapsule) Create(ctx context.Context, req infer.CreateRequest[StorageCapsuleArgs]) (infer.CreateResponse[StorageCapsuleState], error) {
	if req.DryRun {
		return infer.CreateResponse[StorageCapsuleState]{
			ID:     "preview-" + req.Name,
			Output: storageArgsToState(req.Inputs),
		}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, req.Inputs.SpaceID)
	if err != nil {
		return infer.CreateResponse[StorageCapsuleState]{}, err
	}

	manifest := map[string]interface{}{
		"manifestType": "data",
		"dataType":     "PersistentStorage",
		"name":         req.Inputs.Name,
	}
	products := productsFrom(req.Inputs.CpuQty, req.Inputs.MemoryQty, req.Inputs.StorageQty,
		req.Inputs.CpuUnit, req.Inputs.MemoryUnit, req.Inputs.StorageUnit, req.Inputs.Replicas)

	capsule, err := c.CreateCapsule(ctx, endpoint, namespaceKey, req.Inputs.Name, req.Inputs.Description, manifest, products)
	if err != nil {
		return infer.CreateResponse[StorageCapsuleState]{}, err
	}

	return infer.CreateResponse[StorageCapsuleState]{
		ID:     capsule.ID,
		Output: storageArgsToState(req.Inputs),
	}, nil
}

// Read supports `pulumi refresh` only - see MysqlCapsule.Read's comment.
func (s *StorageCapsule) Read(ctx context.Context, req infer.ReadRequest[StorageCapsuleArgs, StorageCapsuleState]) (infer.ReadResponse[StorageCapsuleArgs, StorageCapsuleState], error) {
	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, _, err := resolveSpaceEndpoint(ctx, c, req.State.SpaceID)
	if err != nil {
		return infer.ReadResponse[StorageCapsuleArgs, StorageCapsuleState]{}, err
	}
	capsule, err := c.GetCapsule(ctx, endpoint, req.ID)
	if err != nil {
		return infer.ReadResponse[StorageCapsuleArgs, StorageCapsuleState]{}, err
	}
	return infer.ReadResponse[StorageCapsuleArgs, StorageCapsuleState]{
		ID:     capsule.ID,
		Inputs: req.Inputs,
		State:  storageArgsToState(req.Inputs),
	}, nil
}

func (s *StorageCapsule) Diff(ctx context.Context, req infer.DiffRequest[StorageCapsuleArgs, StorageCapsuleState]) (infer.DiffResponse, error) {
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

func (s *StorageCapsule) Update(ctx context.Context, req infer.UpdateRequest[StorageCapsuleArgs, StorageCapsuleState]) (infer.UpdateResponse[StorageCapsuleState], error) {
	if req.DryRun {
		return infer.UpdateResponse[StorageCapsuleState]{Output: storageArgsToState(req.Inputs)}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, req.State.SpaceID)
	if err != nil {
		return infer.UpdateResponse[StorageCapsuleState]{}, err
	}

	if _, err := c.UpdateCapsuleDescription(ctx, endpoint, namespaceKey, req.ID, req.Inputs.Description); err != nil {
		return infer.UpdateResponse[StorageCapsuleState]{}, err
	}
	products := productsFrom(req.Inputs.CpuQty, req.Inputs.MemoryQty, req.Inputs.StorageQty,
		req.Inputs.CpuUnit, req.Inputs.MemoryUnit, req.Inputs.StorageUnit, req.Inputs.Replicas)
	if _, err := c.UpdateCapsuleProducts(ctx, endpoint, req.ID, products); err != nil {
		return infer.UpdateResponse[StorageCapsuleState]{}, err
	}

	return infer.UpdateResponse[StorageCapsuleState]{Output: storageArgsToState(req.Inputs)}, nil
}

func (s *StorageCapsule) Delete(ctx context.Context, req infer.DeleteRequest[StorageCapsuleState]) (infer.DeleteResponse, error) {
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

func storageArgsToState(args StorageCapsuleArgs) StorageCapsuleState {
	return StorageCapsuleState{
		SpaceID: args.SpaceID, Name: args.Name, Description: args.Description,
		CpuQty: args.CpuQty, CpuUnit: args.CpuUnit,
		MemoryQty: args.MemoryQty, MemoryUnit: args.MemoryUnit,
		StorageQty: args.StorageQty, StorageUnit: args.StorageUnit,
		Replicas: args.Replicas,
	}
}

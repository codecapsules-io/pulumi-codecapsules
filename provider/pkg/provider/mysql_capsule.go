package provider

import (
	"context"
	"errors"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
)

// MysqlCapsule manages a MySQL data capsule - capsule-api's
// `manifestType: "data", dataType: "mysql"` Capsule, created via the same
// polymorphic POST .../capsules endpoint every capsule type uses (see
// capsule-api/src/capsule/types/capsule-type.type.ts and
// capsule-api/src/capsule/services/capsule.service.ts:188's
// manifestType-based cargo dispatch). Create polls to Ready before
// returning - see pollDataCapsuleReady's doc comment for why this is
// load-bearing, not cosmetic.
type MysqlCapsule struct{}

type MysqlCapsuleArgs struct {
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

type MysqlCapsuleState struct {
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

	// PrivateHostname/PrivatePort/PrivateConnectionString are server-assigned
	// on create (db-providers/mysql/mysql.ts's create() sets these directly
	// on jsonManifest) - never Create inputs, always Read-only outputs.
	// Secret-ness must be declared in the `provider` tag namespace, not
	// `pulumi` - see config.go's comment for the pulumi-go-provider#192
	// gotcha this repeats.
	PrivateHostname         string `pulumi:"privateHostname"`
	PrivatePort             string `pulumi:"privatePort"`
	PrivateConnectionString string `pulumi:"privateConnectionString" provider:"secret"`
}

func (*MysqlCapsule) Annotate(a infer.Annotator) {
	a.SetToken("index", "MysqlCapsule")
}

func (args *MysqlCapsuleArgs) Annotate(a infer.Annotator) {
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

func (m *MysqlCapsule) Create(ctx context.Context, req infer.CreateRequest[MysqlCapsuleArgs]) (infer.CreateResponse[MysqlCapsuleState], error) {
	if req.DryRun {
		return infer.CreateResponse[MysqlCapsuleState]{
			ID:     "preview-" + req.Name,
			Output: mysqlArgsToState(req.Inputs, MysqlCapsuleState{}),
		}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, req.Inputs.SpaceID)
	if err != nil {
		return infer.CreateResponse[MysqlCapsuleState]{}, err
	}

	manifest := map[string]interface{}{
		"manifestType": "data",
		"dataType":     "mysql",
		"name":         req.Inputs.Name,
	}
	products := productsFrom(req.Inputs.CpuQty, req.Inputs.MemoryQty, req.Inputs.StorageQty,
		req.Inputs.CpuUnit, req.Inputs.MemoryUnit, req.Inputs.StorageUnit, req.Inputs.Replicas)

	capsule, err := c.CreateCapsule(ctx, endpoint, namespaceKey, req.Inputs.Name, req.Inputs.Description, manifest, products)
	if err != nil {
		return infer.CreateResponse[MysqlCapsuleState]{}, err
	}

	ready, err := pollDataCapsuleReady(ctx, c, endpoint, capsule.ID)
	if err != nil {
		return infer.CreateResponse[MysqlCapsuleState]{}, err
	}

	return infer.CreateResponse[MysqlCapsuleState]{
		ID:     ready.ID,
		Output: mysqlCapsuleToState(req.Inputs, ready),
	}, nil
}

// Read supports `pulumi refresh` only, not cold `pulumi import` - it needs
// req.State.SpaceID (already populated from prior state) to resolve the
// cluster endpoint, mirroring Space.Read's own documented
// UUID-only/no-cold-import limitation.
func (m *MysqlCapsule) Read(ctx context.Context, req infer.ReadRequest[MysqlCapsuleArgs, MysqlCapsuleState]) (infer.ReadResponse[MysqlCapsuleArgs, MysqlCapsuleState], error) {
	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, _, err := resolveSpaceEndpoint(ctx, c, req.State.SpaceID)
	if err != nil {
		return infer.ReadResponse[MysqlCapsuleArgs, MysqlCapsuleState]{}, err
	}
	capsule, err := c.GetCapsule(ctx, endpoint, req.ID)
	if err != nil {
		return infer.ReadResponse[MysqlCapsuleArgs, MysqlCapsuleState]{}, err
	}
	return infer.ReadResponse[MysqlCapsuleArgs, MysqlCapsuleState]{
		ID:     capsule.ID,
		Inputs: req.Inputs,
		State:  mysqlCapsuleToState(req.Inputs, capsule),
	}, nil
}

// Diff is hand-written rather than left to infer's tag-driven default -
// same DeleteBeforeReplace reasoning as team.go/space.go.
func (m *MysqlCapsule) Diff(ctx context.Context, req infer.DiffRequest[MysqlCapsuleArgs, MysqlCapsuleState]) (infer.DiffResponse, error) {
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

func (m *MysqlCapsule) Update(ctx context.Context, req infer.UpdateRequest[MysqlCapsuleArgs, MysqlCapsuleState]) (infer.UpdateResponse[MysqlCapsuleState], error) {
	if req.DryRun {
		return infer.UpdateResponse[MysqlCapsuleState]{Output: mysqlArgsToState(req.Inputs, req.State)}, nil
	}

	c := infer.GetConfig[Config](ctx).mustClient()
	endpoint, namespaceKey, err := resolveSpaceEndpoint(ctx, c, req.State.SpaceID)
	if err != nil {
		return infer.UpdateResponse[MysqlCapsuleState]{}, err
	}

	if _, err := c.UpdateCapsuleDescription(ctx, endpoint, namespaceKey, req.ID, req.Inputs.Description); err != nil {
		return infer.UpdateResponse[MysqlCapsuleState]{}, err
	}
	products := productsFrom(req.Inputs.CpuQty, req.Inputs.MemoryQty, req.Inputs.StorageQty,
		req.Inputs.CpuUnit, req.Inputs.MemoryUnit, req.Inputs.StorageUnit, req.Inputs.Replicas)
	capsule, err := c.UpdateCapsuleProducts(ctx, endpoint, req.ID, products)
	if err != nil {
		return infer.UpdateResponse[MysqlCapsuleState]{}, err
	}

	return infer.UpdateResponse[MysqlCapsuleState]{Output: mysqlCapsuleToState(req.Inputs, capsule)}, nil
}

func (m *MysqlCapsule) Delete(ctx context.Context, req infer.DeleteRequest[MysqlCapsuleState]) (infer.DeleteResponse, error) {
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

func mysqlArgsToState(args MysqlCapsuleArgs, prior MysqlCapsuleState) MysqlCapsuleState {
	return MysqlCapsuleState{
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

func mysqlCapsuleToState(args MysqlCapsuleArgs, capsule *client.Capsule) MysqlCapsuleState {
	s := mysqlArgsToState(args, MysqlCapsuleState{})
	s.PrivateHostname = capsule.PrivateHostname()
	s.PrivatePort = capsule.PrivatePort()
	s.PrivateConnectionString = capsule.PrivateConnectionString()
	return s
}

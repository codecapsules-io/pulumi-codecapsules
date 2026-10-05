package provider

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/client"
)

// capsulePollInterval/MaxInterval/Timeout bound how long a data-type
// capsule's Create polls GET /data-capsule/{id}/status before giving up.
// Declared as vars (not consts) so tests can shrink them - mirrors
// client.Config.baseBackoff's test-only override pattern - rather than
// hardcoding a multi-minute real-world timeout into every lifecycle test.
var (
	capsulePollInterval    = 2 * time.Second
	capsulePollMaxInterval = 15 * time.Second
	capsulePollTimeout     = 10 * time.Minute
)

const capsuleStatusReady = "Ready"

// resolveSpaceEndpoint resolves a Space's per-cluster capsule-api base URL
// and namespaceKey - the two-hop lookup every capsule-level operation needs
// (cli/src/modules/capsule/services/capsule.service.ts's pattern, confirmed
// server-side by api/src/space/services/space.service.ts's SpaceService
// always including the `cluster` association on every Space response).
//
// Called fresh on every CRUD op, never cached, matching Space's own
// resolution model - see the base plan's "no resource ID/URN design"
// critique for why staleness here (a Space renamed/re-clustered after this
// capsule was created) is a known, documented gap rather than a silently
// handled one.
func resolveSpaceEndpoint(ctx context.Context, c *client.Client, spaceID string) (endpoint, namespaceKey string, err error) {
	space, err := c.GetSpace(ctx, spaceID)
	if err != nil {
		return "", "", fmt.Errorf("resolve space %q: %w", spaceID, err)
	}
	if space.Cluster == nil || space.Cluster.ClusterApiEndpoint == "" {
		return "", "", fmt.Errorf("space %q has no resolvable cluster endpoint", spaceID)
	}
	return space.Cluster.ClusterApiEndpoint, space.NamespaceKey, nil
}

// pollDataCapsuleReady polls GET /data-capsule/{id}/status until the
// capsule's jsonManifest.status is Ready, or capsulePollTimeout elapses.
//
// This exists because the platform does not protect a dependent capsule
// (e.g. WordpressCapsule referencing a not-yet-ready MysqlCapsule) against
// an unready database: capsule-api's WordpressCargo.isManifestValid
// (cargo-types/wordpress-cargo/wordpress-cargo.ts) only checks that
// storageCapsuleId/database.mysqlCapsuleId are *present*, never that the
// referenced capsule is Ready, and WordpressCargo.create calls
// deploymentService.applyDeployment immediately with no readiness check.
// Mysql/RedisCapsule's Create must therefore not return until the platform
// itself reports Ready, so that ordinary `dependsOn`/output-chaining in a
// consuming Pulumi program is actually safe to rely on - the safety has to
// live here, not be reinvented by every caller.
//
// There is no "Failed" status in the real API
// (cargo-types/data-cargo/db-providers/{mysql,redis}/*.ts's CapsuleStatus
// enum only has Ready/Starting, plus mysql's backup/restore states) - a
// stuck provisioning attempt looks identical to a slow one from this
// client's point of view, so the only failure mode this can detect is a
// timeout, never a definitive "it failed."
func pollDataCapsuleReady(ctx context.Context, c *client.Client, endpoint, capsuleID string) (*client.Capsule, error) {
	deadline := time.Now().Add(capsulePollTimeout)
	interval := capsulePollInterval
	for {
		capsule, err := c.GetDataCapsuleStatus(ctx, endpoint, capsuleID)
		if err != nil {
			return nil, fmt.Errorf("poll capsule %q status: %w", capsuleID, err)
		}
		if capsule.Status() == capsuleStatusReady {
			return capsule, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("capsule %q did not reach Ready within %s (last status %q)", capsuleID, capsulePollTimeout, capsule.Status())
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		interval *= 2
		if interval > capsulePollMaxInterval {
			interval = capsulePollMaxInterval
		}
	}
}

// productsFrom builds a client.ProductsInput from the common sizing fields
// every capsule type's Pulumi args embed, applying the same per-field unit
// defaults the real platform does when unit is omitted (e.g.
// wordpress-manifest-request.dto.ts's `products.memory.unit || 'M'`), so
// Pulumi callers can omit unit entirely for the common case.
func productsFrom(cpuQty, memQty, storageQty float64, cpuUnit, memUnit, storageUnit string, replicas int) client.ProductsInput {
	if cpuUnit == "" {
		cpuUnit = "m"
	}
	if memUnit == "" {
		memUnit = "M"
	}
	if storageUnit == "" {
		storageUnit = "Gi"
	}
	if replicas == 0 {
		replicas = 1
	}
	p := client.ProductsInput{
		CPU:     client.QtyUnit{Qty: cpuQty, Unit: cpuUnit},
		Memory:  client.QtyUnit{Qty: memQty, Unit: memUnit},
		Storage: client.QtyUnit{Qty: storageQty, Unit: storageUnit},
	}
	p.ReplicaScale.Qty = replicas
	return p
}

// envToConfigs converts a Pulumi-facing env map into capsule-api's
// ConfigEntry list, sorted by key so repeated calls with the same map
// produce byte-identical request bodies (deterministic Diff/test
// assertions, not relying on Go's randomized map iteration order).
func envToConfigs(env map[string]string) []client.ConfigEntry {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]client.ConfigEntry, 0, len(env))
	for _, k := range keys {
		out = append(out, client.ConfigEntry{Key: k, Value: env[k]})
	}
	return out
}

// Each capsule resource (Mysql/Redis/Storage/WordpressCapsule) declares its
// own plain cpuQty/cpuUnit/memoryQty/.../replicas fields directly on its
// Args/State structs - deliberately not factored into a shared embedded
// struct. pulumi-go-provider's `infer` package schema-generates resources by
// reflecting over each resource's own named struct; relying on anonymous
// struct embedding to promote pulumi-tagged fields is an unverified
// assumption this pass didn't want to risk (see the two already-documented
// `pulumi-go-provider` gotchas in team.go/space.go/config.go - this
// framework's rough edges are exactly in reflection-driven schema
// generation). productsFrom below is the one shared piece: a plain function
// every resource's Create/Update calls with its own fields.

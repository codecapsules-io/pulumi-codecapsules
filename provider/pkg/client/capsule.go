package client

import (
	"context"
	"strconv"
)

// QtyUnit mirrors the {qty, unit} shape shared by every field of
// capsule-api's ProductRequestDto
// (capsule-api/src/capsule/dto/{cpu,memory,storage}-request.dto.ts).
type QtyUnit struct {
	Qty  float64 `json:"qty"`
	Unit string  `json:"unit,omitempty"`
}

// ProductsInput mirrors capsule-api's ProductRequestDto
// (capsule-api/src/capsule/dto/product-request.dto.ts) - required on every
// capsule create and on PATCH /capsules/{id}/products, for all four capsule
// types this client manages.
type ProductsInput struct {
	CPU          QtyUnit `json:"cpu"`
	Memory       QtyUnit `json:"memory"`
	Storage      QtyUnit `json:"storage"`
	ReplicaScale struct {
		Qty int `json:"qty"`
	} `json:"replicaScale"`
}

// Capsule mirrors capsule-api's CapsuleResponse
// (capsule-api/src/capsule/dto/capsule-response.dto.ts), narrowed to the
// fields this provider reads. JSONManifest is left as a raw map since its
// shape is polymorphic per capsule type, resolved by capsule-api's per-type
// "cargo" handlers (capsule-api/src/capsule/cargo-types/{data-cargo,
// wordpress-cargo}) - see the manifestString/Status/etc. accessors below for
// the specific keys each type is known to set.
type Capsule struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	NamespaceID  string                 `json:"namespaceId"`
	Type         string                 `json:"type"`
	JSONManifest map[string]interface{} `json:"jsonManifest"`
}

func (c *Capsule) manifestString(key string) string {
	if c.JSONManifest == nil {
		return ""
	}
	v, _ := c.JSONManifest[key].(string)
	return v
}

// Status is the data-type capsules' (mysql/Redis/PersistentStorage)
// provisioning status - "Ready" once usable, "Starting" while
// provisioning (mysql additionally has "Creating Backup"/"Restoring
// Backup"). There is no "Failed" status in the real API; see
// provider.pollDataCapsuleReady's doc comment for the implication.
func (c *Capsule) Status() string { return c.manifestString("status") }

// PrivateHostname/PrivatePort/PrivateConnectionString are set by the mysql
// and Redis providers directly on jsonManifest during create
// (db-providers/{mysql,redis}/*.ts) - never present for other capsule types.
func (c *Capsule) PrivateHostname() string { return c.manifestString("privateHostname") }
func (c *Capsule) PrivateConnectionString() string {
	return c.manifestString("privateConnectionString")
}

func (c *Capsule) PrivatePort() string {
	if c.JSONManifest == nil {
		return ""
	}
	switch v := c.JSONManifest["privatePort"].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

// PublicAccessHostname is set by WordpressManifestRequest.mapToCapsule
// (capsule-api/src/capsule/cargo-types/wordpress-cargo/models/
// wordpress-manifest-request.dto.ts) - the hostname customers reach the
// WordPress site on.
func (c *Capsule) PublicAccessHostname() string { return c.manifestString("publicAccessHostname") }

// ConfigEntry mirrors capsule-api's ConfigRequest
// (capsule-api/src/config/dto/config-request.dto.ts).
type ConfigEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// CreateCapsule mirrors POST
// {clusterApiEndpoint}/namespaces/{namespaceKey}/capsules
// (capsule-api/src/capsule/controllers/post-capsule.controller.ts), which
// every capsule type this client manages goes through - type-specific shape
// lives entirely in manifest, dispatched server-side by manifest.manifestType
// (capsule-api/src/capsule/services/capsule.service.ts:188
// `getCargoType(request.manifest.manifestType)`; "data" for
// mysql/Redis/PersistentStorage, dispatched again inside DataCargo by
// manifest.dataType, or "wordpress").
//
// clusterApiEndpoint is resolved fresh per-call from the owning Space (see
// provider.resolveSpaceEndpoint) - this client never caches it, matching the
// existing Space resource's own no-caching two-hop pattern.
func (c *Client) CreateCapsule(ctx context.Context, clusterAPIEndpoint, namespaceKey, name, description string, manifest map[string]interface{}, products ProductsInput) (*Capsule, error) {
	var out Capsule
	body := map[string]interface{}{
		"name":        name,
		"description": description,
		"manifest":    manifest,
		"products":    products,
	}
	if err := c.doCapsuleAPI(ctx, clusterAPIEndpoint, "POST", "/namespaces/"+namespaceKey+"/capsules", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCapsule mirrors GET {clusterApiEndpoint}/capsule/{capsule_id}
// (capsule-api/src/capsule/controllers/get-capsules.controller.ts
// getCapsuleById - note the singular, no-namespace-prefix route, unlike
// Create/Delete).
func (c *Client) GetCapsule(ctx context.Context, clusterAPIEndpoint, id string) (*Capsule, error) {
	var out Capsule
	if err := c.doCapsuleAPI(ctx, clusterAPIEndpoint, "GET", "/capsule/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDataCapsuleStatus mirrors GET
// {clusterApiEndpoint}/data-capsule/{capsule_id}/status
// (capsule-api/src/capsule/cargo-types/data-cargo/controllers/
// get-data-capsule-status.controller.ts) - the endpoint this provider's
// Mysql/RedisCapsule Create methods poll to Ready.
func (c *Client) GetDataCapsuleStatus(ctx context.Context, clusterAPIEndpoint, id string) (*Capsule, error) {
	var out Capsule
	if err := c.doCapsuleAPI(ctx, clusterAPIEndpoint, "GET", "/data-capsule/"+id+"/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteCapsule mirrors DELETE
// {clusterApiEndpoint}/namespaces/{namespaceKey}/capsules/{capsule_id}
// (capsule-api/src/capsule/controllers/delete-capsule.controller.ts).
func (c *Client) DeleteCapsule(ctx context.Context, clusterAPIEndpoint, namespaceKey, id string) error {
	return c.doCapsuleAPI(ctx, clusterAPIEndpoint, "DELETE", "/namespaces/"+namespaceKey+"/capsules/"+id, nil, nil)
}

// UpdateCapsuleProducts mirrors PATCH
// {clusterApiEndpoint}/capsules/{capsule_id}/products
// (capsule-api/src/capsule/controllers/patch-capsule.controller.ts
// patchCapsuleProduct) - the in-place path for cpu/memory/storage/replica
// sizing changes, on every capsule type.
func (c *Client) UpdateCapsuleProducts(ctx context.Context, clusterAPIEndpoint, id string, products ProductsInput) (*Capsule, error) {
	var out Capsule
	if err := c.doCapsuleAPI(ctx, clusterAPIEndpoint, "PATCH", "/capsules/"+id+"/products", products, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateCapsuleDescription mirrors PATCH
// {clusterApiEndpoint}/namespaces/{namespaceKey}/capsule/{capsule_id}
// (capsule-api/src/capsule/controllers/patch-capsule.controller.ts
// updateCapsule) - the only field that generic update path writes is
// `description` (capsuleRequest.description).
func (c *Client) UpdateCapsuleDescription(ctx context.Context, clusterAPIEndpoint, namespaceKey, id, description string) (*Capsule, error) {
	var out Capsule
	body := map[string]string{"description": description}
	if err := c.doCapsuleAPI(ctx, clusterAPIEndpoint, "PATCH", "/namespaces/"+namespaceKey+"/capsule/"+id, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetCapsuleConfigs mirrors PUT
// {clusterApiEndpoint}/capsules/{capsule_id}/configs
// (capsule-api/src/config/controller/set-configs.controller.ts) - a full
// replace of the capsule's env config, not a merge, matching this client's
// own envToConfigs(map) -> full-list conversion.
func (c *Client) SetCapsuleConfigs(ctx context.Context, clusterAPIEndpoint, id string, configs []ConfigEntry) error {
	body := map[string]interface{}{"configs": configs}
	return c.doCapsuleAPI(ctx, clusterAPIEndpoint, "PUT", "/capsules/"+id+"/configs", body, nil)
}

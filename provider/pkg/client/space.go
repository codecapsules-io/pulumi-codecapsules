package client

import "context"

// Space mirrors the fields of api's Space model that this provider manages.
// See api/src/swagger-doc.json components.schemas.Space and
// api/src/space/services/space.service.ts createSpace/updateSpace.
//
// NamespaceKey is server-assigned during Create (space.service.ts calls
// clusterService().createNamespace synchronously and saves the result before
// returning) - it is a Read-only output, never a Create input.
//
// Cluster is populated on every GET /spaces/{id} response because
// SpaceService's own Sequelize `include` always joins the Cluster
// association (api/src/space/services/space.service.ts: `include: [{model:
// Cluster, as: 'cluster', ...}]`) - this is how capsule-api resources
// resolve their per-cluster endpoint without a second request. It is never
// present on a Create/Update request body.
type Space struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Slug         string   `json:"slug"`
	TeamID       string   `json:"teamId"`
	ClusterID    string   `json:"clusterId"`
	NamespaceKey string   `json:"namespaceKey"`
	Cluster      *Cluster `json:"cluster"`
}

// Cluster is the subset of api's Cluster model
// (api/src/cluster/models/database/clusters.ts) capsule-api resources need:
// the per-cluster capsule-api base URL.
type Cluster struct {
	ID                 string `json:"id"`
	ClusterApiEndpoint string `json:"clusterApiEndpoint"`
}

// CreateSpace mirrors POST /spaces/. name, slug, teamId and clusterId are the
// only accepted inputs (space-service.ts createSpace passes the whole input
// through to Space.create, but validates teamId/clusterId exist first).
func (c *Client) CreateSpace(ctx context.Context, name, slug, teamID, clusterID string) (*Space, error) {
	var s Space
	err := c.do(ctx, "POST", "/spaces/", map[string]string{
		"name":      name,
		"slug":      slug,
		"teamId":    teamID,
		"clusterId": clusterID,
	}, &s)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetSpace mirrors GET /spaces/{spaceId}.
func (c *Client) GetSpace(ctx context.Context, id string) (*Space, error) {
	var s Space
	if err := c.do(ctx, "GET", "/spaces/"+id, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateSpaceName mirrors PATCH /spaces/{spaceId}/name. Only name is mutable
// in place; slug/teamId/clusterId are immutable server-side
// (space-service.ts updateSpace only writes existingSpace.name) and must be
// modeled as replace triggers by the resource layer.
func (c *Client) UpdateSpaceName(ctx context.Context, id, name string) (*Space, error) {
	var s Space
	err := c.do(ctx, "PATCH", "/spaces/"+id+"/name", map[string]string{"name": name}, &s)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteSpace mirrors DELETE /spaces/{spaceId}/delete. The backend rejects
// deletion (400) if the space still has active Capsules
// (space-service.ts validateSpaceHasNoCapsules) - surfaced as *APIError with
// IsBadRequest() true.
func (c *Client) DeleteSpace(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/spaces/"+id+"/delete", nil, nil)
}

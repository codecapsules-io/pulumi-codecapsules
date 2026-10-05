package client

import "context"

// Team mirrors the fields of api's Team model that this provider manages.
// See api/src/swagger-doc.json components.schemas.Team and
// api/src/services/team-service.ts createTeam/updateTeam.
type Team struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// CreateTeam mirrors POST /teams/ (api/src/api/team/team-routes.ts). Only
// name and slug are accepted on create; customerKey is server-assigned.
func (c *Client) CreateTeam(ctx context.Context, name, slug string) (*Team, error) {
	var t Team
	err := c.do(ctx, "POST", "/teams/", map[string]string{"name": name, "slug": slug}, &t)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTeam mirrors GET /teams/{slug_or_id}.
func (c *Client) GetTeam(ctx context.Context, idOrSlug string) (*Team, error) {
	var t Team
	if err := c.do(ctx, "GET", "/teams/"+idOrSlug, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTeam mirrors PATCH /teams/update. Only name is mutable in place; slug
// is immutable server-side (team-service.ts updateTeam only writes team.name)
// and must be modeled as a replace trigger by the resource layer.
func (c *Client) UpdateTeam(ctx context.Context, id, name string) (*Team, error) {
	var t Team
	err := c.do(ctx, "PATCH", "/teams/update", map[string]string{"id": id, "name": name}, &t)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTeam mirrors DELETE /teams/{teamId}. The backend rejects deletion
// (400) if the team still has active Spaces
// (team-service.ts validateTeamHasNoActiveSpaces) - surfaced to the caller as
// an *APIError with IsBadRequest() true, not swallowed.
func (c *Client) DeleteTeam(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/teams/"+id, nil, nil)
}

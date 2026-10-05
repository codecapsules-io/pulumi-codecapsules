// Package testutil provides a fake in-memory implementation of the pieces of
// `api`'s HTTP surface this provider depends on (Team, Space CRUD), so the
// provider and its resources can be exercised in fast unit/lifecycle tests
// without a real backend. Behavior (which fields are mutable, which
// preconditions block delete, response envelope shape) is modeled directly
// off api/src/services/team-service.ts and api/src/services/space-service.ts,
// not guessed - see the comments on each handler.
package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

type Team struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Deleted bool   `json:"-"`
}

type Space struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	TeamID       string `json:"teamId"`
	ClusterID    string `json:"clusterId"`
	NamespaceKey string `json:"namespaceKey"`
	Deleted      bool   `json:"-"`

	// Cluster is populated lazily by New()/SeedSpace to the fake server's own
	// URL, so a Space created or seeded against this fake server resolves its
	// "cluster endpoint" straight back to the same server - capsule-api
	// routes below are served on that same mux, so tests need only one
	// httptest.Server, not two. Mirrors the real `cluster` association
	// always-included on GET /spaces/{id} (api/src/space/services/
	// space.service.ts).
	Cluster *Cluster `json:"cluster"`
}

// Cluster mirrors the subset of api's Cluster model capsule-api resources
// need: the per-cluster capsule-api base URL.
type Cluster struct {
	ID                 string `json:"id"`
	ClusterApiEndpoint string `json:"clusterApiEndpoint"`
}

// Capsule is a fake stand-in for capsule-api's Capsule entity. One entity,
// `type`-discriminated like the real platform (capsule-api/src/capsule/
// types/capsule-type.type.ts) - not split into separate fake models per
// type. JSONManifest holds whatever the per-type create logic below sets,
// mirroring the real jsonManifest polymorphism.
type Capsule struct {
	ID           string
	Name         string
	Description  string
	NamespaceKey string
	Type         string // "mysql" | "Redis" | "PersistentStorage" | "wordpress"
	JSONManifest map[string]interface{}
	Configs      map[string]string
	Deleted      bool
	// pollsUntilReady counts down on each GET .../status call for data
	// types; status flips to Ready once it reaches 0. Lets a test simulate
	// "slow-then-ready" instead of every capsule being immediately Ready.
	pollsUntilReady int
}

// Fault lets a test force the next N matching requests to fail before normal
// handling runs, to exercise the client's retry/error-mapping paths (429s,
// transient 5xxs) without needing a second real backend.
type Fault struct {
	Method     string
	PathPrefix string
	Status     int
	Body       string
	Remaining  int // number of times to trigger before falling through
}

// FakeServer is a minimal stand-in for `api`'s Team/Space endpoints.
// Not thread-safe across concurrent mutation from multiple test goroutines
// beyond what its internal mutex covers - fine for the sequential lifecycle
// tests it's built for.
type FakeServer struct {
	*httptest.Server

	mu                 sync.Mutex
	teams              map[string]*Team
	spaces             map[string]*Space
	capsules           map[string]*Capsule
	nextID             int
	requireAuth        bool
	spacesWithCapsules map[string]bool // simulates space-service.ts validateSpaceHasNoCapsules
	teamsWithSpaces    map[string]bool // simulates team-service.ts validateTeamHasNoActiveSpaces
	faults             []*Fault
}

func New() *FakeServer {
	s := &FakeServer{
		teams:              map[string]*Team{},
		spaces:             map[string]*Space{},
		capsules:           map[string]*Capsule{},
		requireAuth:        true,
		spacesWithCapsules: map[string]bool{},
		teamsWithSpaces:    map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/teams/", s.handleTeams)
	mux.HandleFunc("/teams/update", s.handleTeamUpdate)
	mux.HandleFunc("/spaces/", s.handleSpaces)
	// capsule-api-shaped routes, served on the same fake server as `api` -
	// see the Space.Cluster doc comment above for why one server is enough.
	mux.HandleFunc("/namespaces/", s.handleNamespaceCapsules)
	mux.HandleFunc("/capsule/", s.handleCapsuleSingular)
	mux.HandleFunc("/capsules/", s.handleCapsulesPlural)
	mux.HandleFunc("/data-capsule/", s.handleDataCapsuleStatus)
	s.Server = httptest.NewServer(withAuthCheck(s, mux))
	return s
}

func withAuthCheck(s *FakeServer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		requireAuth := s.requireAuth
		s.mu.Unlock()
		if requireAuth && r.Header.Get("Authorization") == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		if fault := s.consumeFault(r); fault != nil {
			writeError(w, fault.Status, fault.Body)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth toggles whether the fake server enforces the Authorization
// header, for tests that specifically exercise the 401 path.
func (s *FakeServer) RequireAuth(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requireAuth = v
}

// InjectFault queues a fault that fires on the next matching request(s).
func (s *FakeServer) InjectFault(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, &f)
}

func (s *FakeServer) consumeFault(r *http.Request) *Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.faults {
		if f.Remaining > 0 && f.Method == r.Method && hasPrefix(r.URL.Path, f.PathPrefix) {
			f.Remaining--
			return f
		}
	}
	return nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// MarkSpaceHasCapsules simulates the space having active Capsules, which
// makes DeleteSpace fail with 400 just like the real backend does.
func (s *FakeServer) MarkSpaceHasCapsules(spaceID string, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spacesWithCapsules[spaceID] = v
}

// MarkTeamHasSpaces simulates the team still owning active Spaces, which
// makes DeleteTeam fail with 400 just like the real backend does.
func (s *FakeServer) MarkTeamHasSpaces(teamID string, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teamsWithSpaces[teamID] = v
}

// SeedTeam creates a team directly against server state, bypassing HTTP, so
// tests can set up fixtures without depending on the provider's own Create
// path (which is exactly what's under test elsewhere).
func (s *FakeServer) SeedTeam(name, slug string) *Team {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &Team{ID: s.newID("team"), Name: name, Slug: slug}
	s.teams[t.ID] = t
	return t
}

// SeedSpace creates a space directly against server state, bypassing HTTP,
// for the same reason SeedTeam does.
func (s *FakeServer) SeedSpace(name, slug, teamID, clusterID string) *Space {
	s.mu.Lock()
	defer s.mu.Unlock()
	team := s.teams[teamID]
	namespaceKey := name
	if team != nil {
		namespaceKey = team.Slug + "-" + slug
	}
	sp := &Space{
		ID: s.newID("space"), Name: name, Slug: slug,
		TeamID: teamID, ClusterID: clusterID, NamespaceKey: namespaceKey,
		Cluster: &Cluster{ID: clusterID, ClusterApiEndpoint: s.Server.URL},
	}
	s.spaces[sp.ID] = sp
	return sp
}

// SetCapsuleReadyAfter configures a data-type capsule to report "Starting"
// for the next n GET .../status polls before flipping to "Ready" - lets a
// test exercise pollDataCapsuleReady's actual poll loop (slow-then-ready)
// instead of every capsule being immediately Ready. n=0 (the default for
// any newly created data capsule) means Ready on the very first poll.
func (s *FakeServer) SetCapsuleReadyAfter(capsuleID string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.capsules[capsuleID]; ok {
		c.pollsUntilReady = n
	}
}

func (s *FakeServer) newID(prefix string) string {
	s.nextID++
	return fmt.Sprintf("%s-%04d", prefix, s.nextID)
}

func writeEnvelope(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data, "meta": map[string]interface{}{}})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// writeRaw writes an un-enveloped JSON body - capsule-api (LoopBack4)
// returns its DTOs directly, unlike `api`'s `{ data }` wrapper (see
// client.doCapsuleAPI's doc comment).
func writeRaw(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func capsuleJSON(c *Capsule) map[string]interface{} {
	return map[string]interface{}{
		"id":           c.ID,
		"name":         c.Name,
		"description":  c.Description,
		"namespaceId":  c.NamespaceKey,
		"type":         c.Type,
		"jsonManifest": c.JSONManifest,
	}
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// --- Capsules (capsule-api-shaped routes) -----------------------------

// handleNamespaceCapsules serves the namespace-scoped capsule-api routes:
// POST .../namespaces/{namespaceKey}/capsules (create),
// DELETE .../namespaces/{namespaceKey}/capsules/{id} (delete), and
// PATCH .../namespaces/{namespaceKey}/capsule/{id} (description-only update
// - note the singular "capsule").
func (s *FakeServer) handleNamespaceCapsules(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path[len("/namespaces/"):])
	switch {
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "capsules":
		s.createCapsule(w, r, parts[0])
	case r.Method == http.MethodDelete && len(parts) == 3 && parts[1] == "capsules":
		s.deleteCapsule(w, r, parts[2])
	case r.Method == http.MethodPatch && len(parts) == 3 && parts[1] == "capsule":
		s.updateCapsuleDescription(w, r, parts[2])
	default:
		writeError(w, http.StatusNotFound, "no route")
	}
}

// createCapsule models capsule-api's PostCapsuleController.createCapsule +
// the per-type cargo handlers it dispatches to via manifest.manifestType
// (capsule-api/src/capsule/services/capsule.service.ts:188) - "data" for
// mysql/Redis/PersistentStorage (further dispatched by manifest.dataType),
// "wordpress" for WordPress. Mysql/Redis are modeled as async (status
// starts "Starting", see SetCapsuleReadyAfter); PersistentStorage is
// synchronous (status "Ready" immediately), matching
// persistent-storage.ts's create(). WordPress sets no status field at all,
// matching wordpress-cargo.ts having no async machinery.
func (s *FakeServer) createCapsule(w http.ResponseWriter, r *http.Request, namespaceKey string) {
	var body struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Manifest    map[string]interface{} `json:"manifest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	namespaceExists := false
	for _, sp := range s.spaces {
		if !sp.Deleted && sp.NamespaceKey == namespaceKey {
			namespaceExists = true
			break
		}
	}
	if !namespaceExists {
		writeError(w, http.StatusNotFound, "Namespace not found")
		return
	}

	manifest := map[string]interface{}{}
	for k, v := range body.Manifest {
		manifest[k] = v
	}

	manifestType, _ := body.Manifest["manifestType"].(string)
	var capsuleType string
	switch manifestType {
	case "data":
		dataType, _ := body.Manifest["dataType"].(string)
		capsuleType = dataType
		switch dataType {
		case "mysql", "Redis":
			manifest["status"] = "Starting"
			manifest["privateHostname"] = fmt.Sprintf("%s-%s", strings.ToLower(dataType), body.Name)
			manifest["privatePort"] = 3306.0
			manifest["privateConnectionString"] = fmt.Sprintf("%s://user:pass@%s-%s/db", strings.ToLower(dataType), strings.ToLower(dataType), body.Name)
		case "PersistentStorage":
			manifest["status"] = "Ready" // synchronous - matches rook-nfs/persistent-storage.ts
		default:
			writeError(w, http.StatusBadRequest, "unsupported dataType")
			return
		}
	case "wordpress":
		capsuleType = "wordpress"
		manifest["publicAccessHostname"] = body.Name + ".test.ccdns.co"
	default:
		writeError(w, http.StatusBadRequest, "unsupported manifestType")
		return
	}

	c := &Capsule{
		ID: s.newID("capsule"), Name: body.Name, Description: body.Description,
		NamespaceKey: namespaceKey, Type: capsuleType, JSONManifest: manifest,
		Configs: map[string]string{},
	}
	if configsRaw, ok := body.Manifest["configs"].([]interface{}); ok {
		for _, entry := range configsRaw {
			m, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			k, _ := m["key"].(string)
			v, _ := m["value"].(string)
			c.Configs[k] = v
		}
	}
	s.capsules[c.ID] = c
	writeRaw(w, http.StatusOK, capsuleJSON(c))
}

func (s *FakeServer) deleteCapsule(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.capsules[id]
	if !ok || c.Deleted {
		writeError(w, http.StatusNotFound, "capsule not found")
		return
	}
	c.Deleted = true
	w.WriteHeader(http.StatusNoContent)
}

func (s *FakeServer) updateCapsuleDescription(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.capsules[id]
	if !ok || c.Deleted {
		writeError(w, http.StatusNotFound, "capsule not found")
		return
	}
	c.Description = body.Description
	writeRaw(w, http.StatusOK, capsuleJSON(c))
}

// handleCapsuleSingular serves GET /capsule/{id} (note: singular, no
// namespace prefix - capsule-api/src/capsule/controllers/
// get-capsules.controller.ts getCapsuleById).
func (s *FakeServer) handleCapsuleSingular(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/capsule/"):]
	if r.Method != http.MethodGet || id == "" {
		writeError(w, http.StatusNotFound, "no route")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.capsules[id]
	if !ok || c.Deleted {
		writeError(w, http.StatusNotFound, "capsule not found")
		return
	}
	writeRaw(w, http.StatusOK, capsuleJSON(c))
}

// handleCapsulesPlural serves PATCH /capsules/{id}/products and
// GET/PUT /capsules/{id}/configs (note: plural, no namespace prefix).
func (s *FakeServer) handleCapsulesPlural(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path[len("/capsules/"):])
	switch {
	case r.Method == http.MethodPatch && len(parts) == 2 && parts[1] == "products":
		s.updateCapsuleProducts(w, r, parts[0])
	case r.Method == http.MethodPut && len(parts) == 2 && parts[1] == "configs":
		s.setCapsuleConfigs(w, r, parts[0])
	case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "configs":
		s.getCapsuleConfigs(w, r, parts[0])
	default:
		writeError(w, http.StatusNotFound, "no route")
	}
}

func (s *FakeServer) updateCapsuleProducts(w http.ResponseWriter, r *http.Request, id string) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.capsules[id]
	if !ok || c.Deleted {
		writeError(w, http.StatusNotFound, "capsule not found")
		return
	}
	writeRaw(w, http.StatusOK, capsuleJSON(c))
}

func (s *FakeServer) setCapsuleConfigs(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Configs []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"configs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.capsules[id]
	if !ok || c.Deleted {
		writeError(w, http.StatusNotFound, "capsule not found")
		return
	}
	c.Configs = map[string]string{}
	for _, e := range body.Configs {
		c.Configs[e.Key] = e.Value
	}
	writeRaw(w, http.StatusOK, map[string]interface{}{"configs": body.Configs})
}

func (s *FakeServer) getCapsuleConfigs(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.capsules[id]
	if !ok || c.Deleted {
		writeError(w, http.StatusNotFound, "capsule not found")
		return
	}
	configs := make([]map[string]string, 0, len(c.Configs))
	for k, v := range c.Configs {
		configs = append(configs, map[string]string{"key": k, "value": v})
	}
	writeRaw(w, http.StatusOK, map[string]interface{}{"configs": configs})
}

// handleDataCapsuleStatus serves GET /data-capsule/{id}/status - the
// endpoint pollDataCapsuleReady polls. See SetCapsuleReadyAfter for how a
// test simulates a slow-to-provision capsule.
func (s *FakeServer) handleDataCapsuleStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.URL.Path[len("/data-capsule/"):], "/status")
	if r.Method != http.MethodGet {
		writeError(w, http.StatusNotFound, "no route")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.capsules[id]
	if !ok || c.Deleted {
		writeError(w, http.StatusBadRequest, "Invalid capsule_id")
		return
	}
	if c.JSONManifest["status"] != "Ready" {
		if c.pollsUntilReady > 0 {
			c.pollsUntilReady--
		} else {
			c.JSONManifest["status"] = "Ready"
		}
	}
	writeRaw(w, http.StatusOK, capsuleJSON(c))
}

// --- Teams -----------------------------------------------------------------

func (s *FakeServer) handleTeams(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/teams/"):]

	switch {
	case r.Method == http.MethodPost && path == "":
		s.createTeam(w, r)
	case r.Method == http.MethodGet && path != "":
		s.getTeam(w, r, path)
	case r.Method == http.MethodDelete && path != "":
		s.deleteTeam(w, r, path)
	default:
		writeError(w, http.StatusNotFound, "no route")
	}
}

func (s *FakeServer) createTeam(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Slug string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.teams {
		if !t.Deleted && t.Slug == body.Slug {
			writeError(w, http.StatusConflict, "slug already exists")
			return
		}
	}
	t := &Team{ID: s.newID("team"), Name: body.Name, Slug: body.Slug}
	s.teams[t.ID] = t
	writeEnvelope(w, http.StatusOK, t)
}

func (s *FakeServer) getTeam(w http.ResponseWriter, r *http.Request, idOrSlug string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.teams[idOrSlug]; ok && !t.Deleted {
		writeEnvelope(w, http.StatusOK, t)
		return
	}
	for _, t := range s.teams {
		if !t.Deleted && t.Slug == idOrSlug {
			writeEnvelope(w, http.StatusOK, t)
			return
		}
	}
	writeError(w, http.StatusNotFound, "team not found")
}

func (s *FakeServer) handleTeamUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusNotFound, "no route")
		return
	}
	var body struct{ ID, Name string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.teams[body.ID]
	if !ok || t.Deleted {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}
	t.Name = body.Name // slug is intentionally not writable here - matches team-service.ts updateTeam
	writeEnvelope(w, http.StatusOK, t)
}

func (s *FakeServer) deleteTeam(w http.ResponseWriter, r *http.Request, teamID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.teams[teamID]
	if !ok || t.Deleted {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}
	if s.teamsWithSpaces[teamID] {
		writeError(w, http.StatusBadRequest, "Cannot delete a team which has active Spaces")
		return
	}
	t.Deleted = true
	writeEnvelope(w, http.StatusOK, map[string]interface{}{})
}

// --- Spaces ------------------------------------------------------------

func (s *FakeServer) handleSpaces(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/spaces/"):]

	switch {
	case r.Method == http.MethodPost && path == "":
		s.createSpace(w, r)
	case r.Method == http.MethodDelete && hasSuffix(path, "/delete"):
		s.deleteSpace(w, r, trimSuffix(path, "/delete"))
	case r.Method == http.MethodPatch && hasSuffix(path, "/name"):
		s.updateSpaceName(w, r, trimSuffix(path, "/name"))
	case r.Method == http.MethodGet && path != "":
		s.getSpace(w, r, path)
	default:
		writeError(w, http.StatusNotFound, "no route")
	}
}

func hasSuffix(s, suf string) bool    { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }
func trimSuffix(s, suf string) string { return s[:len(s)-len(suf)] }

func (s *FakeServer) createSpace(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Slug, TeamID, ClusterID string }
	dec := json.NewDecoder(r.Body)
	var raw map[string]string
	if err := dec.Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	body.Name, body.Slug, body.TeamID, body.ClusterID = raw["name"], raw["slug"], raw["teamId"], raw["clusterId"]

	s.mu.Lock()
	defer s.mu.Unlock()
	team, ok := s.teams[body.TeamID]
	if !ok || team.Deleted {
		writeError(w, http.StatusBadRequest, "Invalid team")
		return
	}
	for _, sp := range s.spaces {
		if !sp.Deleted && sp.Slug == body.Slug {
			writeError(w, http.StatusConflict, "slug already exists")
			return
		}
	}
	sp := &Space{
		ID:           s.newID("space"),
		Name:         body.Name,
		Slug:         body.Slug,
		TeamID:       body.TeamID,
		ClusterID:    body.ClusterID,
		NamespaceKey: fmt.Sprintf("%s-%s", team.Slug, body.Slug), // matches real namespaceKey shape
		Cluster:      &Cluster{ID: body.ClusterID, ClusterApiEndpoint: s.Server.URL},
	}
	s.spaces[sp.ID] = sp
	writeEnvelope(w, http.StatusOK, sp)
}

func (s *FakeServer) getSpace(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spaces[id]
	if !ok || sp.Deleted {
		writeError(w, http.StatusNotFound, "space not found")
		return
	}
	writeEnvelope(w, http.StatusOK, sp)
}

func (s *FakeServer) updateSpaceName(w http.ResponseWriter, r *http.Request, id string) {
	var raw map[string]string
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spaces[id]
	if !ok || sp.Deleted {
		writeError(w, http.StatusNotFound, "space not found")
		return
	}
	sp.Name = raw["name"]
	writeEnvelope(w, http.StatusOK, sp)
}

func (s *FakeServer) deleteSpace(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spaces[id]
	if !ok || sp.Deleted {
		writeError(w, http.StatusNotFound, "space not found")
		return
	}
	if s.spacesWithCapsules[id] {
		writeError(w, http.StatusBadRequest, "Cannot delete a space which has active Capsules")
		return
	}
	sp.Deleted = true
	writeEnvelope(w, http.StatusOK, map[string]interface{}{})
}

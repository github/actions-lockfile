// Package server implements a fake Launch receiver that speaks the same
// protocol as the real runner expects. Resolves actions via GitHub GraphQL API.
package fakelaunch

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/github/actions-lockfile/pkg/resolver"
	"github.com/github/actions-lockfile/pkg/lockfile"
)

// ActionReferenceRequest matches the runner's ActionReferenceRequest shape.
type ActionReferenceRequest struct {
	Action  string `json:"action"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

// ActionReferenceRequestList matches ActionReferenceRequestList.
type ActionReferenceRequestList struct {
	Actions []ActionReferenceRequest `json:"actions"`
}

// ActionDownloadInfoResponse matches the runner's expected response shape.
type ActionDownloadInfoResponse struct {
	Name        string                              `json:"name"`
	ResolvedName string                             `json:"resolved_name"`
	ResolvedSha string                              `json:"resolved_sha"`
	TarURL      string                              `json:"tar_url"`
	ZipURL      string                              `json:"zip_url"`
	Version     string                              `json:"version"`
	Auth        *ActionDownloadAuthenticationResponse `json:"authentication,omitempty"`
}

type ActionDownloadAuthenticationResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// ActionDownloadInfoResponseCollection is the top-level response.
type ActionDownloadInfoResponseCollection struct {
	Actions map[string]ActionDownloadInfoResponse `json:"actions"`
}

// Server is the fake Launch receiver.
type Server struct {
	token    string
	resolver *resolver.Client
	port     int
}

// New creates a fake Launch server.
func New(token string, port int) *Server {
	return &Server{
		token:    token,
		resolver: resolver.New(token),
		port:     port,
	}
}

// Run starts the server.
func (s *Server) Run() error {
	mux := http.NewServeMux()

	// The runner POSTs to /actions/build/{planId}/jobs/{jobId}/runnerresolve/actions
	mux.HandleFunc("/", s.handleDefault)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("Fake Launch server listening on %s", addr)
	log.Printf("Set system.github.launch_endpoint=http://localhost%s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleDefault(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if strings.HasSuffix(path, "/_apis/connectionData") {
		s.handleConnectionData(w, r)
		return
	}

	if strings.HasSuffix(path, "/completejob") {
		s.handleCompleteJob(w, r)
		return
	}

	if strings.Contains(path, "/_apis/distributedtask") ||
		strings.Contains(path, "/_apis/pipelines") {
		s.handleStub(w, r)
		return
	}

	if strings.HasSuffix(path, "/runnerresolve/actions") ||
		strings.HasSuffix(path, "/resolve/actions") {
		s.handleResolveActions(w, r)
		return
	}

	log.Printf("[UNHANDLED] %s %s", r.Method, path)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte(`{}`))
}

func (s *Server) handleConnectionData(w http.ResponseWriter, r *http.Request) {
	log.Printf("[STUB] %s %s (connection data)", r.Method, r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"locationServiceData": map[string]interface{}{
			"serviceDefinitions": []interface{}{},
		},
		"instanceId":      "00000000-0000-0000-0000-000000000000",
		"deploymentId":    "00000000-0000-0000-0000-000000000000",
		"authenticatedUser": map[string]interface{}{
			"id":                "00000000-0000-0000-0000-000000000000",
			"descriptor":        "System:00000000-0000-0000-0000-000000000000",
			"providerDisplayName": "test",
		},
		"authorizedUser": map[string]interface{}{
			"id":                "00000000-0000-0000-0000-000000000000",
			"descriptor":        "System:00000000-0000-0000-0000-000000000000",
			"providerDisplayName": "test",
		},
	})
}

func (s *Server) handleStub(w http.ResponseWriter, r *http.Request) {
	log.Printf("[STUB] %s %s", r.Method, r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte(`{}`))
}

func (s *Server) handleCompleteJob(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("[COMPLETE] failed to parse: %v", err)
		w.WriteHeader(200)
		return
	}

	names := []string{"Succeeded", "SucceededWithIssues", "Failed", "Cancelled", "Skipped", "Abandoned"}

	resolveName := func(v interface{}) string {
		switch val := v.(type) {
		case float64:
			if int(val) >= 0 && int(val) < len(names) {
				return names[int(val)]
			}
		case string:
			return val
		}
		return fmt.Sprintf("%v", v)
	}

	conclusion := resolveName(body["conclusion"])

	var stepResults []interface{}
	if sr, ok := body["stepResults"].([]interface{}); ok {
		stepResults = sr
	}

	color := "\033[31m" // red
	if strings.EqualFold(conclusion, "succeeded") || strings.EqualFold(conclusion, "succeededWithIssues") {
		color = "\033[32m" // green
	}
	log.Printf("%s[COMPLETE] Job %s (%d steps)\033[0m", color, conclusion, len(stepResults))

	for i, sr := range stepResults {
		step, ok := sr.(map[string]interface{})
		if !ok {
			continue
		}
		name := step["name"]
		result := resolveName(step["conclusion"])
		icon := "\033[31m\u2717" // red X
		if strings.EqualFold(result, "succeeded") || strings.EqualFold(result, "succeededWithIssues") {
			icon = "\033[32m\u2713" // green check
		}
		log.Printf("  %s Step %d: %v -- %s\033[0m", icon, i+1, name, result)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte(`{}`))
}

func (s *Server) handleResolveActions(w http.ResponseWriter, r *http.Request) {
	// Accept any path that ends with /runnerresolve/actions or /resolve/actions
	if !strings.HasSuffix(r.URL.Path, "/runnerresolve/actions") &&
		!strings.HasSuffix(r.URL.Path, "/resolve/actions") {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ActionReferenceRequestList
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}

	log.Printf("Resolving %d action(s):", len(req.Actions))
	for _, a := range req.Actions {
		path := ""
		if a.Path != "" {
			path = "/" + a.Path
		}
		log.Printf("  %s%s@%s", a.Action, path, a.Version)
	}

	// Convert to our internal ActionRef format
	var refs []lockfile.ActionRef
	for _, a := range req.Actions {
		parts := strings.SplitN(a.Action, "/", 2)
		if len(parts) != 2 {
			continue
		}
		refs = append(refs, lockfile.ActionRef{
			Owner: parts[0],
			Repo:  parts[1],
			Path:  a.Path,
			Ref:   a.Version,
			Raw:   fmt.Sprintf("%s@%s", a.Action, a.Version),
		})
	}

	// Resolve via GraphQL
	deps, err := s.resolver.ResolveAll(refs)
	if err != nil {
		log.Printf("Resolution error: %v", err)
		http.Error(w, fmt.Sprintf("resolution failed: %v", err), http.StatusUnprocessableEntity)
		return
	}

	// Build response in runner-expected format
	// Match deps to input refs by NWO (GraphQL alias order may differ from input order)
	resp := ActionDownloadInfoResponseCollection{
		Actions: make(map[string]ActionDownloadInfoResponse),
	}

	depsByNWO := make(map[string]lockfile.Dependency)
	for _, dep := range deps {
		depsByNWO[dep.NWO+"@"+dep.Ref] = dep
	}

	for _, ref := range refs {
		key := ref.Raw
		nwo := ref.NWO()
		lookupKey := nwo + "@" + ref.Ref

		dep, ok := depsByNWO[lookupKey]
		if !ok {
			log.Printf("  [WARN] no resolution for %s", lookupKey)
			continue
		}

		tarURL := fmt.Sprintf("https://api.github.com/repos/%s/tarball/%s", nwo, dep.SHA)
		zipURL := fmt.Sprintf("https://api.github.com/repos/%s/zipball/%s", nwo, dep.SHA)

		resp.Actions[key] = ActionDownloadInfoResponse{
			Name:         nwo,
			ResolvedName: nwo,
			ResolvedSha:  dep.SHA,
			TarURL:       tarURL,
			ZipURL:       zipURL,
			Version:      ref.Ref,
		}

		log.Printf("  -> %s@%s = %s", nwo, ref.Ref, dep.SHA[:12])
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

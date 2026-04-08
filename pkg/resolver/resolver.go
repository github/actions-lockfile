// Package resolver handles resolving action refs to commit SHAs via GitHub's GraphQL API.
package resolver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/github/actions-lockfile/pkg/actionmeta"
	"github.com/github/actions-lockfile/pkg/lockfile"
)

// DefaultMaxRecursionDepth matches Constants.CompositeActionsMaxDepth in the runner.
const DefaultMaxRecursionDepth = 10

// Client resolves action refs to commit SHAs via GitHub's GraphQL API.
type Client struct {
	token             string
	httpClient        *http.Client
	apiURL            string
	MaxRecursionDepth int // configurable, defaults to DefaultMaxRecursionDepth
}

// New creates a new resolver client.
func New(token string) *Client {
	return &Client{
		token:             token,
		httpClient:        http.DefaultClient,
		apiURL:            "https://api.github.com/graphql",
		MaxRecursionDepth: DefaultMaxRecursionDepth,
	}
}

// ResolveAll resolves a batch of action refs to their commit SHAs.
// Does NOT recurse into composite actions.
func (c *Client) ResolveAll(refs []lockfile.ActionRef) ([]lockfile.Dependency, error) {
	if len(refs) == 0 {
		return nil, nil
	}

	query, aliasMap := buildResolveQuery(refs)
	resp, err := c.doGraphQL(query)
	if err != nil {
		return nil, err
	}

	return parseResolveResponse(resp, refs, aliasMap)
}

// ResolveAllRecursive resolves action refs and recursively discovers transitive
// dependencies from composite actions by reading their action.yml via GraphQL.
func (c *Client) ResolveAllRecursive(refs []lockfile.ActionRef) ([]lockfile.Dependency, error) {
	seen := make(map[string]bool) // nwo@ref -> already resolved
	var allDeps []lockfile.Dependency

	pending := refs
	depth := 0

	for len(pending) > 0 {
		if depth >= c.MaxRecursionDepth {
			return allDeps, fmt.Errorf("composite action recursion exceeded max depth %d", c.MaxRecursionDepth)
		}

		// Deduplicate against already-resolved (by full name including path)
		var toResolve []lockfile.ActionRef
		for _, ref := range pending {
			key := ref.FullName() + "@" + ref.Ref
			if !seen[key] {
				seen[key] = true
				toResolve = append(toResolve, ref)
			}
		}

		if len(toResolve) == 0 {
			break
		}

		// Resolve this batch + fetch action.yml for each
		deps, actionYMLs, err := c.resolveWithActionYML(toResolve)
		if err != nil {
			return allDeps, err
		}
		allDeps = append(allDeps, deps...)

		// Parse action.yml for each resolved dep, collect nested uses: from composites
		var nextPending []lockfile.ActionRef
		for i := range deps {
			yml := actionYMLs[i]
			if yml == "" {
				continue
			}

			meta, err := actionmeta.Parse(yml)
			if err != nil {
				continue
			}

			if meta.Execution != actionmeta.ExecComposite {
				continue
			}

			for _, uses := range meta.NestedUses {
				ar := lockfile.ParseActionRef(uses)
				if ar == nil {
					continue
				}
				nextPending = append(nextPending, *ar)
			}
		}

		pending = nextPending
		depth++
	}

	// Deduplicate final dependency list by NWO@ref (path actions share the same repo SHA)
	seenDeps := make(map[string]bool)
	var dedupDeps []lockfile.Dependency
	for _, d := range allDeps {
		key := d.Key()
		if !seenDeps[key] {
			seenDeps[key] = true
			dedupDeps = append(dedupDeps, d)
		}
	}

	return dedupDeps, nil
}

// MaxBatchSize is the maximum number of action refs per GraphQL query.
// Matches the internal ResolveActionsBatchSize.
const MaxBatchSize = 20

// resolveWithActionYML resolves refs and also fetches action.yml content for each.
// Returns deps and the corresponding action.yml content (empty string if not found).
// Batches into groups of MaxBatchSize to stay within GraphQL node limits.
func (c *Client) resolveWithActionYML(refs []lockfile.ActionRef) ([]lockfile.Dependency, []string, error) {
	if len(refs) <= MaxBatchSize {
		return c.resolveWithActionYMLBatch(refs)
	}

	var allDeps []lockfile.Dependency
	var allYMLs []string
	for i := 0; i < len(refs); i += MaxBatchSize {
		end := i + MaxBatchSize
		if end > len(refs) {
			end = len(refs)
		}
		deps, ymls, err := c.resolveWithActionYMLBatch(refs[i:end])
		if err != nil {
			return allDeps, allYMLs, err
		}
		allDeps = append(allDeps, deps...)
		allYMLs = append(allYMLs, ymls...)
	}
	return allDeps, allYMLs, nil
}

func (c *Client) resolveWithActionYMLBatch(refs []lockfile.ActionRef) ([]lockfile.Dependency, []string, error) {
	query, aliasMap := buildResolveWithFileQuery(refs)
	resp, err := c.doGraphQL(query)
	if err != nil {
		return nil, nil, err
	}

	return parseResolveWithFileResponse(resp, refs, aliasMap)
}

type graphqlRequest struct {
	Query string `json:"query"`
}

type graphqlResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []graphqlError             `json:"errors"`
}

type graphqlError struct {
	Message string   `json:"message"`
	Type    string   `json:"type"`
	Path    []string `json:"path"`
}

type repoResponse struct {
	NameWithOwner string `json:"nameWithOwner"`
	Object        *struct {
		OID  string `json:"oid"`
		File *struct {
			Object *struct {
				Text string `json:"text"`
			} `json:"object"`
		} `json:"file"`
		FileYAML *struct {
			Object *struct {
				Text string `json:"text"`
			} `json:"object"`
		} `json:"fileYaml"`
	} `json:"object"`
}

func buildResolveQuery(refs []lockfile.ActionRef) (string, map[string]int) {
	aliasMap := make(map[string]int)
	var sb strings.Builder
	sb.WriteString("query {\n")

	for i, ref := range refs {
		alias := fmt.Sprintf("a%d", i)
		aliasMap[alias] = i
		escapedRef := strings.ReplaceAll(ref.Ref, `"`, `\"`)
		sb.WriteString(fmt.Sprintf("  %s: repository(owner: %q, name: %q) {\n", alias, ref.Owner, ref.Repo))
		sb.WriteString("    nameWithOwner\n")
		sb.WriteString(fmt.Sprintf("    object(expression: %q) {\n", escapedRef))
		sb.WriteString("      ... on Commit { oid }\n")
		sb.WriteString("    }\n")
		sb.WriteString("  }\n")
	}

	sb.WriteString("}\n")
	return sb.String(), aliasMap
}

func buildResolveWithFileQuery(refs []lockfile.ActionRef) (string, map[string]int) {
	aliasMap := make(map[string]int)
	var sb strings.Builder
	sb.WriteString("query {\n")

	for i, ref := range refs {
		alias := fmt.Sprintf("a%d", i)
		aliasMap[alias] = i
		escapedRef := strings.ReplaceAll(ref.Ref, `"`, `\"`)

		// Determine the action.yml path based on whether this is a path action
		ymlPath := "action.yml"
		yamlPath := "action.yaml"
		if ref.Path != "" {
			ymlPath = ref.Path + "/action.yml"
			yamlPath = ref.Path + "/action.yaml"
		}

		sb.WriteString(fmt.Sprintf("  %s: repository(owner: %q, name: %q) {\n", alias, ref.Owner, ref.Repo))
		sb.WriteString("    nameWithOwner\n")
		sb.WriteString(fmt.Sprintf("    object(expression: %q) {\n", escapedRef))
		sb.WriteString("      ... on Commit {\n")
		sb.WriteString("        oid\n")
		sb.WriteString(fmt.Sprintf("        file: file(path: %q) {\n", ymlPath))
		sb.WriteString("          object { ... on Blob { text } }\n")
		sb.WriteString("        }\n")
		sb.WriteString(fmt.Sprintf("        fileYaml: file(path: %q) {\n", yamlPath))
		sb.WriteString("          object { ... on Blob { text } }\n")
		sb.WriteString("        }\n")
		sb.WriteString("      }\n")
		sb.WriteString("    }\n")
		sb.WriteString("  }\n")
	}

	sb.WriteString("}\n")
	return sb.String(), aliasMap
}

func (c *Client) doGraphQL(query string) (*graphqlResponse, error) {
	reqBody, err := json.Marshal(graphqlRequest{Query: query})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequest("POST", c.apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GraphQL API returned %d: %s", resp.StatusCode, string(body))
	}

	var gqlResp graphqlResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	// Only fail on top-level errors (not field-scoped)
	for _, e := range gqlResp.Errors {
		if len(e.Path) == 0 {
			return nil, fmt.Errorf("GraphQL error: %s", e.Message)
		}
	}

	return &gqlResp, nil
}

func parseResolveResponse(resp *graphqlResponse, refs []lockfile.ActionRef, aliasMap map[string]int) ([]lockfile.Dependency, error) {
	var deps []lockfile.Dependency
	var errs []string

	for alias, idx := range aliasMap {
		ref := refs[idx]
		raw, ok := resp.Data[alias]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s@%s: not found in response", ref.NWO(), ref.Ref))
			continue
		}

		if string(raw) == "null" {
			errs = append(errs, fmt.Sprintf("%s@%s: repository not found or not accessible", ref.NWO(), ref.Ref))
			continue
		}

		var repo repoResponse
		if err := json.Unmarshal(raw, &repo); err != nil {
			errs = append(errs, fmt.Sprintf("%s@%s: failed to parse: %v", ref.NWO(), ref.Ref, err))
			continue
		}

		if repo.Object == nil || repo.Object.OID == "" {
			errs = append(errs, fmt.Sprintf("%s@%s: ref %q does not exist", ref.NWO(), ref.Ref, ref.Ref))
			continue
		}

		deps = append(deps, lockfile.Dependency{
			NWO: ref.NWO(),
			Ref: ref.Ref,
			SHA: repo.Object.OID,
		})
	}

	if len(errs) > 0 {
		return deps, fmt.Errorf("resolution errors:\n  %s", strings.Join(errs, "\n  "))
	}

	return deps, nil
}

func parseResolveWithFileResponse(resp *graphqlResponse, refs []lockfile.ActionRef, aliasMap map[string]int) ([]lockfile.Dependency, []string, error) {
	var deps []lockfile.Dependency
	var ymls []string
	var errs []string

	for alias, idx := range aliasMap {
		ref := refs[idx]
		raw, ok := resp.Data[alias]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s@%s: not found in response", ref.NWO(), ref.Ref))
			continue
		}

		if string(raw) == "null" {
			errs = append(errs, fmt.Sprintf("%s@%s: repository not found or not accessible", ref.NWO(), ref.Ref))
			continue
		}

		var repo repoResponse
		if err := json.Unmarshal(raw, &repo); err != nil {
			errs = append(errs, fmt.Sprintf("%s@%s: failed to parse: %v", ref.NWO(), ref.Ref, err))
			continue
		}

		if repo.Object == nil || repo.Object.OID == "" {
			errs = append(errs, fmt.Sprintf("%s@%s: ref %q does not exist", ref.NWO(), ref.Ref, ref.Ref))
			continue
		}

		dep := lockfile.Dependency{
			NWO: ref.NWO(),
			Ref: ref.Ref,
			SHA: repo.Object.OID,
		}
		deps = append(deps, dep)

		// Extract action.yml or action.yaml content (prefer .yml)
		var yml string
		if repo.Object.File != nil && repo.Object.File.Object != nil {
			yml = repo.Object.File.Object.Text
		} else if repo.Object.FileYAML != nil && repo.Object.FileYAML.Object != nil {
			yml = repo.Object.FileYAML.Object.Text
		}
		ymls = append(ymls, yml)
	}

	if len(errs) > 0 {
		return deps, ymls, fmt.Errorf("resolution errors:\n  %s", strings.Join(errs, "\n  "))
	}

	return deps, ymls, nil
}

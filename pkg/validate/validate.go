// Package validate implements the `actions-lockfile validate` command.
package validate

import (
	"fmt"
	"strings"
	"os"

	"github.com/github/actions-lockfile/pkg/resolver"
	"github.com/github/actions-lockfile/pkg/lockfile"
)

// Result holds the validation outcome.
type Result struct {
	Valid    bool
	Errors   []string
	Warnings []string
}

// Run validates that pinned dependencies in a workflow file still match live resolution.
func Run(path string, token string) (*Result, error) {
	result := &Result{Valid: true}

	// Load and parse the workflow
	wf, err := lockfile.Load(path)
	if err != nil {
		return nil, fmt.Errorf("loading workflow: %w", err)
	}

	// Read existing dependencies
	existingDeps, err := wf.ReadDependencies()
	if err != nil {
		return nil, fmt.Errorf("reading dependencies: %w", err)
	}

	if len(existingDeps) == 0 {
		return nil, fmt.Errorf("no dependencies: section found in %s -- run `actions-lockfile pin` first", path)
	}

	// Extract action references from the workflow
	refs, localPaths, parseWarnings := wf.ExtractActionRefs()
	result.Warnings = append(result.Warnings, parseWarnings...)

	// Discover transitive deps from local composite actions
	if len(localPaths) > 0 {
		localRefs, localWarnings := lockfile.ExtractLocalCompositeRefs(path, localPaths)
		result.Warnings = append(result.Warnings, localWarnings...)
		refs = append(refs, localRefs...)
	}

	// Check that every uses: ref has a corresponding dependency entry
	depsByNWO := make(map[string]lockfile.Dependency)
	for _, d := range existingDeps {
		depsByNWO[d.Key()] = d
	}

	for _, ref := range refs {
		key := ref.NWO() + "@" + ref.Ref
		if _, ok := depsByNWO[key]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("MISSING: %s@%s is used in workflow but not in dependencies:", ref.NWO(), ref.Ref))
		}
	}

	// Re-resolve all refs (with composite recursion) and compare
	fmt.Fprintf(os.Stderr, "Re-resolving %d action reference(s) (with composite recursion)...\n", len(refs))
	client := resolver.New(token)
	liveDeps, err := client.ResolveAllRecursive(refs)
	if err != nil {
		return nil, fmt.Errorf("resolving actions: %w", err)
	}

	liveByKey := make(map[string]lockfile.Dependency)
	for _, d := range liveDeps {
		liveByKey[d.Key()] = d
	}

	// Compare each existing dependency against live resolution
	for _, existing := range existingDeps {
		live, ok := liveByKey[existing.Key()]
		if !ok {
			// Dependency in lockfile but not discoverable from uses: refs
			// This could be a stale entry or an injected dependency.
			// Fail by default -- if it's a legitimate transitive dep,
			// re-pinning will rediscover it.
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("STALE: %s is in dependencies: but not discoverable from workflow uses: refs", existing.Key()))
			continue
		}

		if existing.SHA != live.SHA {
			result.Valid = false
			// Sanitize SHAs in error output (prevent log injection)
			safeStoredSHA := sanitizeForLog(existing.SHA)
			safeLiveSHA := sanitizeForLog(live.SHA)
			result.Errors = append(result.Errors,
				fmt.Sprintf("TAMPERED: %s -- pinned sha1-%s but resolved to sha1-%s",
					existing.Key(), safeStoredSHA, safeLiveSHA))
		}
	}

	return result, nil
}

// sanitizeForLog removes control characters from strings before including in log output.
func sanitizeForLog(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteRune('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

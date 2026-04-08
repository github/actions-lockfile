// Package pin implements the `actions-lockfile pin` command.
package pin

import (
	"fmt"
	"os"
	"sort"

	"github.com/github/actions-lockfile/pkg/resolver"
	"github.com/github/actions-lockfile/pkg/lockfile"
)

// Options controls pin behavior.
type Options struct {
	DryRun bool // resolve and print without writing
	Diff   bool // show what changed vs existing deps
}

// Run pins all repository action refs in a workflow file.
func Run(path string, token string, opts Options) error {
	// Load and parse the workflow
	wf, err := lockfile.Load(path)
	if err != nil {
		return fmt.Errorf("loading workflow: %w", err)
	}

	// Check for existing dependencies section
	existingDeps, err := wf.ReadDependencies()
	if err != nil {
		return fmt.Errorf("reading existing dependencies: %w", err)
	}

	// Extract action references
	refs, localPaths, warnings := wf.ExtractActionRefs()
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	// Discover transitive deps from local composite actions
	if len(localPaths) > 0 {
		localRefs, localWarnings := lockfile.ExtractLocalCompositeRefs(path, localPaths)
		for _, w := range localWarnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		refs = append(refs, localRefs...)
	}

	if len(refs) == 0 {
		fmt.Fprintf(os.Stderr, "No repository action references found in %s\n", path)
		return nil
	}

	fmt.Fprintf(os.Stderr, "Resolving %d action reference(s)...\n", len(refs))
	for _, ref := range refs {
		fmt.Fprintf(os.Stderr, "  %s@%s\n", ref.NWO(), ref.Ref)
	}

	// Resolve all refs to SHAs, recursing into composite actions
	client := resolver.New(token)
	deps, err := client.ResolveAllRecursive(refs)
	if err != nil {
		return fmt.Errorf("resolving actions: %w", err)
	}

	// If there's an existing dependencies section, validate consistency
	if len(existingDeps) > 0 {
		oldByKey := make(map[string]lockfile.Dependency)
		for _, d := range existingDeps {
			oldByKey[d.Key()] = d
		}
		newByKey := make(map[string]lockfile.Dependency)
		for _, d := range deps {
			newByKey[d.Key()] = d
		}

		// Fail on SHA changes (possible force-pushed tags)
		var shaChanges []string
		for _, d := range deps {
			if old, ok := oldByKey[d.Key()]; ok && old.SHA != d.SHA {
				shaChanges = append(shaChanges,
					fmt.Sprintf("  %s: %s -> %s", d.Key(), old.SHA[:12], d.SHA[:12]))
			}
		}
		if len(shaChanges) > 0 {
			fmt.Fprintf(os.Stderr, "error: SHA changed for pinned dependencies (tag may have been force-pushed):\n")
			for _, c := range shaChanges {
				fmt.Fprintf(os.Stderr, "%s\n", c)
			}
			return fmt.Errorf("%d dependency SHA(s) changed since last pin -- investigate before updating", len(shaChanges))
		}

		var staleErrors []string
		for _, existing := range existingDeps {
			if _, ok := newByKey[existing.Key()]; !ok {
				staleErrors = append(staleErrors,
					fmt.Sprintf("  %s: was in dependencies but not discoverable from current uses: refs", existing.Key()))
			}
		}

		if len(staleErrors) > 0 {
			fmt.Fprintf(os.Stderr, "error: existing dependencies section has entries that can't be resolved:\n")
			for _, e := range staleErrors {
				fmt.Fprintf(os.Stderr, "%s\n", e)
			}
			return fmt.Errorf("stale entries in existing dependencies: section -- remove them manually or pass --allow-partial")
		}
	}

	// Show diff if requested
	if opts.Diff && len(existingDeps) > 0 {
		showDiff(existingDeps, deps)
	}

	// Dry run -- print what would be written and exit
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "Resolved %d dependencies (dry run, not writing):\n", len(deps))
		sort.Slice(deps, func(i, j int) bool { return deps[i].String() < deps[j].String() })
		for _, d := range deps {
			fmt.Fprintf(os.Stderr, "  %s\n", d.String())
		}
		return nil
	}

	// Write updated workflow with dependencies section
	output, err := wf.WriteDependencies(deps)
	if err != nil {
		return fmt.Errorf("writing dependencies: %w", err)
	}

	if err := os.WriteFile(path, output, 0644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Pinned %d dependencies in %s\n", len(deps), path)
	for _, d := range deps {
		fmt.Fprintf(os.Stderr, "  %s\n", d.String())
	}

	return nil
}

func showDiff(old, new []lockfile.Dependency) {
	oldMap := make(map[string]lockfile.Dependency)
	for _, d := range old {
		oldMap[d.Key()] = d
	}
	newMap := make(map[string]lockfile.Dependency)
	for _, d := range new {
		newMap[d.Key()] = d
	}

	// Added
	for _, d := range new {
		if _, ok := oldMap[d.Key()]; !ok {
			fmt.Fprintf(os.Stderr, "  \033[32m+ %s\033[0m\n", d.String())
		}
	}

	// Changed SHA
	for _, d := range new {
		if o, ok := oldMap[d.Key()]; ok && o.SHA != d.SHA {
			fmt.Fprintf(os.Stderr, "  \033[33m~ %s\033[0m\n", d.Key())
			fmt.Fprintf(os.Stderr, "    \033[31m- sha1-%s\033[0m\n", o.SHA)
			fmt.Fprintf(os.Stderr, "    \033[32m+ sha1-%s\033[0m\n", d.SHA)
		}
	}

	// Removed
	for _, d := range old {
		if _, ok := newMap[d.Key()]; !ok {
			fmt.Fprintf(os.Stderr, "  \033[31m- %s\033[0m\n", d.String())
		}
	}

	// Unchanged count
	unchanged := 0
	for _, d := range new {
		if o, ok := oldMap[d.Key()]; ok && o.SHA == d.SHA {
			unchanged++
		}
	}
	if unchanged > 0 {
		fmt.Fprintf(os.Stderr, "  \033[2m%d unchanged\033[0m\n", unchanged)
	}
}

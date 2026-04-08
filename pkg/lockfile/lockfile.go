// Package workflow handles parsing and modifying workflow YAML files.
// Extracts action references (uses:) and manages the dependencies: section.
//
// Terminology aligns with the runner codebase (actions/runner):
//   - ActionRef     ~ RepositoryPathReference (owner, repo, path, ref)
//   - Dependency    ~ ActionDownloadInfo (nwo, ref, resolved sha)
//   - ExecComposite ~ ActionExecutionType.Composite
package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ActionRef represents a parsed uses: reference to a repository action.
// Corresponds to RepositoryPathReference in the runner SDK.
type ActionRef struct {
	Owner string // e.g. "actions"
	Repo  string // e.g. "checkout"
	Path  string // e.g. "save" (for actions/cache/save@v4), empty for root actions
	Ref   string // e.g. "v4" -- tag, branch, or full SHA
	Raw   string // original uses: string
}

// NWO returns owner/repo (Name With Owner).
func (a ActionRef) NWO() string {
	if a.Owner == "" && a.Repo == "" {
		return ""
	}
	return a.Owner + "/" + a.Repo
}

// FullName returns owner/repo or owner/repo/path.
// Used for deduplication during recursive resolution.
func (a ActionRef) FullName() string {
	if a.Path != "" {
		return a.Owner + "/" + a.Repo + "/" + a.Path
	}
	return a.Owner + "/" + a.Repo
}

// Dependency represents a pinned dependency entry in the dependencies: section.
// Corresponds to ActionDownloadInfo in the runner SDK.
type Dependency struct {
	// OPEN QUESTION: should the NWO include the "github.com/" prefix in the
	// serialized format? The v0.2 design doc uses it (e.g. "github.com/actions/checkout@v4:sha1-...")
	// but it's redundant for github.com-only resolution. Left as-is for now
	// pending format finalization.
	NWO      string // owner/repo (e.g. "actions/checkout")
	Ref      string // resolved ref as given in uses:
	SHA      string // full commit hash
	HashAlgo string // "sha1" or "sha256"
}

// Key returns the dependency key for deduplication.
func (d Dependency) Key() string {
	return d.NWO + "@" + d.Ref
}

// String formats as the YAML list entry.
// Format: github.com/owner/repo@ref:sha1-HASH (or :sha256-HASH)
func (d Dependency) String() string {
	algo := d.HashAlgo
	if algo == "" {
		algo = detectHashAlgo(d.SHA)
	}
	return fmt.Sprintf("github.com/%s@%s:%s-%s", d.NWO, d.Ref, algo, d.SHA)
}

// ParseDependencyString parses a dependency entry string back into a Dependency.
// Accepts both "sha1-" and "sha256-" prefixed hashes.
func ParseDependencyString(s string) (Dependency, error) {
	s = strings.TrimPrefix(s, "github.com/")

	var sha string
	var algo string
	var nwoRefPart string

	if idx := strings.Index(s, ":sha256-"); idx >= 0 {
		nwoRefPart = s[:idx]
		sha = s[idx+len(":sha256-"):]
		algo = "sha256"
	} else if idx := strings.Index(s, ":sha1-"); idx >= 0 {
		nwoRefPart = s[:idx]
		sha = s[idx+len(":sha1-"):]
		algo = "sha1"
	} else {
		return Dependency{}, fmt.Errorf("invalid dependency format (expected :sha1- or :sha256-): %q", s)
	}

	nwoRef := strings.SplitN(nwoRefPart, "@", 2)
	if len(nwoRef) != 2 {
		return Dependency{}, fmt.Errorf("invalid dependency nwo@ref: %q", nwoRefPart)
	}

	return Dependency{
		NWO:      nwoRef[0],
		Ref:      nwoRef[1],
		SHA:      sha,
		HashAlgo: algo,
	}, nil
}

// detectHashAlgo guesses the hash algorithm from the hash length.
func detectHashAlgo(hash string) string {
	if len(hash) == 64 {
		return "sha256"
	}
	return "sha1"
}

// ParseActionRef parses a uses: string into an ActionRef.
// Only handles repository actions (owner/repo@ref and owner/repo/path@ref).
// Returns nil for local path actions (./), docker actions (docker://),
// expression-based refs (${{), and reusable workflow refs (.github/workflows/).
func ParseActionRef(uses string) *ActionRef {
	uses = strings.TrimSpace(uses)

	// Skip local path actions
	if strings.HasPrefix(uses, "./") {
		return nil
	}
	// Skip docker actions
	if strings.HasPrefix(uses, "docker://") {
		return nil
	}
	// Skip expression-based uses: (can't statically resolve)
	if strings.Contains(uses, "${") {
		return nil
	}

	// Must have exactly one @
	atParts := strings.SplitN(uses, "@", 2)
	if len(atParts) != 2 || atParts[1] == "" {
		return nil
	}
	ref := atParts[1]

	// Split the nwo/path part
	segments := strings.SplitN(atParts[0], "/", 3)
	if len(segments) < 2 {
		return nil
	}

	ar := &ActionRef{
		Owner: segments[0],
		Repo:  segments[1],
		Ref:   ref,
		Raw:   uses,
	}
	if len(segments) == 3 {
		ar.Path = segments[2]
	}

	// Reusable workflow references resolve the same way as actions.
	// Keep the path so the dependency key distinguishes them.

	return ar
}

// File represents a parsed workflow file with its raw content.
type File struct {
	Path    string
	Content []byte
	root    yaml.Node
}

// Load reads and parses a workflow file.
func Load(path string) (*File, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading workflow: %w", err)
	}

	f := &File{
		Path:    path,
		Content: content,
	}

	if err := yaml.Unmarshal(content, &f.root); err != nil {
		return nil, fmt.Errorf("parsing workflow YAML: %w", err)
	}

	return f, nil
}

// ExtractActionRefs finds all uses: references to repository actions in the workflow.
// Deduplicates by NWO@ref (path actions with same repo share a single resolution).
// Returns the refs, local action paths (./), and any warnings about unpinnable entries.
func (f *File) ExtractActionRefs() ([]ActionRef, []string, []string) {
	var refs []ActionRef
	var warnings []string
	var localPaths []string
	seen := make(map[string]bool)
	seenLocal := make(map[string]bool)

	walkYAML(&f.root, func(key, value string) {
		if key == "uses" {
			value = strings.TrimSpace(value)
			if strings.Contains(value, "${") {
				warnings = append(warnings, fmt.Sprintf("can't pin expression-based uses: %s", value))
				return
			}
			if strings.HasPrefix(value, "./") {
				if !seenLocal[value] {
					seenLocal[value] = true
					localPaths = append(localPaths, value)
				}
				return
			}
			ar := ParseActionRef(value)
			if ar != nil {
				dedupKey := ar.NWO() + "@" + ar.Ref
				if !seen[dedupKey] {
					seen[dedupKey] = true
					refs = append(refs, *ar)
				}
			}
		}
	})

	return refs, localPaths, warnings
}

// ReadDependencies extracts the current dependencies: section from the workflow.
// Rejects duplicate keys and entries with control characters (injection defense).
func (f *File) ReadDependencies() ([]Dependency, error) {
	var deps []Dependency
	seen := make(map[string]bool)

	doc := docNode(&f.root)
	if doc == nil {
		return nil, nil
	}

	for i := 0; i < len(doc.Content)-1; i += 2 {
		if doc.Content[i].Value == "dependencies" {
			seq := doc.Content[i+1]
			if seq.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("dependencies: must be a sequence")
			}
			for _, item := range seq.Content {
				if strings.ContainsAny(item.Value, "\n\r\t") {
					return nil, fmt.Errorf("dependency entry contains control characters (possible injection)")
				}
				d, err := ParseDependencyString(item.Value)
				if err != nil {
					return nil, fmt.Errorf("parsing dependency entry: %w", err)
				}
				if seen[d.Key()] {
					return nil, fmt.Errorf("duplicate dependency entry for %s", d.Key())
				}
				seen[d.Key()] = true
				deps = append(deps, d)
			}
			return deps, nil
		}
	}

	return nil, nil
}

// WriteDependencies writes the workflow file back with an updated dependencies: section.
// Output is deterministic (sorted entries).
func (f *File) WriteDependencies(deps []Dependency) ([]byte, error) {
	content := string(f.Content)

	sort.Slice(deps, func(i, j int) bool {
		return deps[i].String() < deps[j].String()
	})

	var sb strings.Builder
	sb.WriteString("\n# Automatically generated and managed by: `actions-lockfile pin <workflow-path>`\n")
	sb.WriteString("dependencies:\n")
	for _, d := range deps {
		sb.WriteString("  - " + d.String() + "\n")
	}

	content = removeDependenciesSection(content)
	content = strings.TrimRight(content, "\n") + "\n"
	content += sb.String()

	return []byte(content), nil
}

// removeDependenciesSection strips an existing dependencies: block from the YAML string.
func removeDependenciesSection(content string) string {
	re := regexp.MustCompile(`(?m)^\n?# Automatically generated and managed by:.*\ndependencies:\n(?:  - .*\n)*`)
	content = re.ReplaceAllString(content, "")

	re2 := regexp.MustCompile(`(?m)^dependencies:\n(?:  - .*\n)*`)
	content = re2.ReplaceAllString(content, "")

	return content
}

// walkYAML walks a YAML node tree, calling fn for each scalar key-value pair.
func walkYAML(node *yaml.Node, fn func(key, value string)) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			walkYAML(child, fn)
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content)-1; i += 2 {
			key := node.Content[i]
			val := node.Content[i+1]
			if key.Kind == yaml.ScalarNode && val.Kind == yaml.ScalarNode {
				fn(key.Value, val.Value)
			}
			walkYAML(val, fn)
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			walkYAML(child, fn)
		}
	}
}

// docNode returns the root mapping node from a document.
func docNode(root *yaml.Node) *yaml.Node {
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		return root.Content[0]
	}
	if root.Kind == yaml.MappingNode {
		return root
	}
	return nil
}

// ExtractLocalCompositeRefs reads action.yml files from local paths relative
// to the workflow file's directory, and returns any repository action refs
// found in their steps. This discovers transitive deps from ./local composites.
func ExtractLocalCompositeRefs(workflowPath string, localPaths []string) ([]ActionRef, []string) {
	var refs []ActionRef
	var warnings []string
	seen := make(map[string]bool)

	// The workflow file is at e.g. .github/workflows/ci.yml
	// Local paths are relative to the repo root (./my-action)
	// We need the repo root to resolve them
	repoRoot := findRepoRoot(workflowPath)
	if repoRoot == "" {
		if len(localPaths) > 0 {
			warnings = append(warnings, "can't resolve local action paths: not in a git repository")
		}
		return nil, warnings
	}

	for _, localPath := range localPaths {
		// Strip ./ prefix
		relPath := strings.TrimPrefix(localPath, "./")
		actionDir := filepath.Join(repoRoot, relPath)

		// Try action.yml then action.yaml
		var actionContent []byte
		var err error
		actionContent, err = os.ReadFile(filepath.Join(actionDir, "action.yml"))
		if err != nil {
			actionContent, err = os.ReadFile(filepath.Join(actionDir, "action.yaml"))
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("can't read action file for %s: %v", localPath, err))
				continue
			}
		}

		// Parse the action.yml to find uses: refs
		meta, err := parseActionYAMLForUses(actionContent)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("can't parse action file for %s: %v", localPath, err))
			continue
		}

		for _, uses := range meta {
			ar := ParseActionRef(uses)
			if ar != nil {
				dedupKey := ar.NWO() + "@" + ar.Ref
				if !seen[dedupKey] {
					seen[dedupKey] = true
					refs = append(refs, *ar)
				}
			}
		}
	}

	return refs, warnings
}

// findRepoRoot walks up from the given path to find the .git directory.
func findRepoRoot(startPath string) string {
	absPath, err := filepath.Abs(filepath.Dir(startPath))
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(absPath, ".git")); err == nil {
			return absPath
		}
		parent := filepath.Dir(absPath)
		if parent == absPath {
			return ""
		}
		absPath = parent
	}
}

// parseActionYAMLForUses extracts uses: values from a composite action YAML.
func parseActionYAMLForUses(content []byte) ([]string, error) {
	var action struct {
		Runs struct {
			Using string `yaml:"using"`
			Steps []struct {
				Uses string `yaml:"uses"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(content, &action); err != nil {
		return nil, err
	}
	if action.Runs.Using != "composite" {
		return nil, nil
	}
	var uses []string
	for _, step := range action.Runs.Steps {
		if step.Uses != "" {
			uses = append(uses, step.Uses)
		}
	}
	return uses, nil
}

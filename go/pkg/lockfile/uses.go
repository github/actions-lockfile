package lockfile

import "strings"

// ActionRef is a parsed `uses:` reference to a repository action.
// It captures only the components the lockfile grammar cares about:
// owner, repo, optional sub-action path, ref string, and the original
// raw value for diagnostics.
//
// ParseActionRef is the only constructor; consumers should treat zero
// values as invalid.
type ActionRef struct {
	Owner string // e.g. "actions"
	Repo  string // e.g. "checkout"
	Path  string // e.g. "save" for actions/cache/save@v4
	Ref   string // tag, branch, or full SHA as written after `@`
	Raw   string // original `uses:` string (post-trim)
}

// NWO returns owner/repo (Name With Owner). Zero-value ActionRefs return
// the empty string.
func (a ActionRef) NWO() string {
	if a.Owner == "" && a.Repo == "" {
		return ""
	}
	return a.Owner + "/" + a.Repo
}

// FullName returns owner/repo or owner/repo/path. Used for human-facing
// display and for graph traversal where distinct sub-paths must be
// treated as distinct nodes.
func (a ActionRef) FullName() string {
	if a.Path != "" {
		return a.Owner + "/" + a.Repo + "/" + a.Path
	}
	return a.Owner + "/" + a.Repo
}

// ParseActionRef parses a `uses:` string into an ActionRef. It returns
// nil for any input that is not a repository action — expression-based
// refs, local paths, docker images, reusable workflow files, or any
// input containing control characters that would otherwise reach
// downstream URL/GraphQL builders.
//
// The returned pointer is non-nil iff the input names a real repository
// action (composite or javascript) at owner/repo[/path]@ref.
func ParseActionRef(uses string) *ActionRef {
	uses = strings.TrimSpace(uses)

	if uses == "" || containsControlChars(uses) {
		return nil
	}
	if strings.HasPrefix(uses, "./") {
		return nil
	}
	if strings.HasPrefix(uses, "docker://") {
		return nil
	}
	if strings.Contains(uses, "${") {
		return nil
	}

	atParts := strings.SplitN(uses, "@", 2)
	if len(atParts) != 2 || atParts[1] == "" {
		return nil
	}
	ref := atParts[1]

	segments := strings.SplitN(atParts[0], "/", 3)
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		return nil
	}
	if !isValidSegment(segments[0]) || !isValidSegment(segments[1]) {
		return nil
	}

	actionRef := &ActionRef{
		Owner: segments[0],
		Repo:  segments[1],
		Ref:   ref,
		Raw:   uses,
	}
	if len(segments) == 3 {
		if !isValidPath(segments[2]) {
			return nil
		}
		actionRef.Path = segments[2]
	}

	if isReusableWorkflow(actionRef) {
		return nil
	}

	return actionRef
}

// isValidSegment enforces the GitHub character set for owner names, repository
// names, and action path segments. GitHub allows alphanumerics, hyphens,
// underscores, and periods; reject anything else to keep these values safe for
// use in URL paths and GraphQL string literals without per-call escaping bugs.
func isValidSegment(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

func containsControlChars(s string) bool {
	for _, c := range s {
		if c <= 0x1F || c == 0x7F {
			return true
		}
	}
	return false
}

func isYAMLFile(path string) bool {
	return strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml")
}

// isValidPath validates the subdirectory path segment of a uses: reference.
// Each segment must be non-empty, not "." or "..", and contain only the
// characters valid in GitHub repository paths (alphanumerics, hyphens,
// underscores, and periods). This extends the security boundary from
// owner/repo into the path component.
func isValidPath(p string) bool {
	if p == "" {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return false
		}
		if !isValidSegment(seg) {
			return false
		}
	}
	return true
}

// isReusableWorkflow reports whether a parsed ActionRef points at a
// reusable workflow file rather than a repository action. Reusable
// workflows are keyed by `<owner>/<repo>/.github/workflows/<name>.yml`.
//
// Anchor on prefix: substring matching would misclassify composite
// actions whose nested folder happens to contain that segment (e.g.
// `tools/.github/workflows/`).
func isReusableWorkflow(actionRef *ActionRef) bool {
	if actionRef.Path == "" {
		return false
	}
	if !strings.HasPrefix(actionRef.Path, ".github/workflows/") {
		return false
	}
	return isYAMLFile(actionRef.Path)
}

// IsLocalReusableWorkflow reports whether a `./...`-prefixed local
// `uses:` value names a reusable workflow file (rather than a local
// composite action directory). Exposed for consumers that walk
// workflows themselves and need to distinguish the two shapes.
func IsLocalReusableWorkflow(localPath string) bool {
	return isYAMLFile(localPath)
}

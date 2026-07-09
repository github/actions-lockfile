package lockfile

import "strings"

// ActionRef is a parsed `uses:` reference to a repository action.
//
// ParseActionRef is the only constructor; treat zero values as invalid.
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

// FullName returns owner/repo, or owner/repo/path when a sub-action path is
// present.
func (a ActionRef) FullName() string {
	if a.Path != "" {
		return a.Owner + "/" + a.Repo + "/" + a.Path
	}
	return a.Owner + "/" + a.Repo
}

// ParseActionRef parses a `uses:` string into an ActionRef. It returns nil
// for anything that is not a repository action — expression refs, local
// paths, docker images, reusable workflow files, or any input whose
// components are unsafe for the downstream URL/GraphQL builders.
func ParseActionRef(uses string) *ActionRef {
	parsed := splitUsesRef(uses)
	if parsed == nil {
		return nil
	}
	// A YAML file under .github/workflows/ is never a repository action:
	// the direct-child form is a reusable workflow, anything nested is
	// malformed.
	if isWorkflowFile(parsed.Path) {
		return nil
	}
	return &ActionRef{
		Owner: parsed.Owner,
		Repo:  parsed.Repo,
		Path:  parsed.Path,
		Ref:   parsed.Ref,
		Raw:   parsed.Raw,
	}
}

// usesRef carries the components splitUsesRef extracts before classification.
// It is deliberately not an ActionRef, since a freshly split ref may turn out
// to be a reusable workflow.
type usesRef struct {
	Owner string
	Repo  string
	Path  string
	Ref   string
	Raw   string
}

// splitUsesRef performs the parse shared by ParseActionRef and
// ParseReusableWorkflowRef: prefilter the input, split at the FIRST `@` (so a
// ref containing `@`, e.g. a branch named "release@2024", stays intact), then
// validate the ref and owner/repo[/path] segments. It does not classify the
// result as action vs reusable workflow.
func splitUsesRef(uses string) *usesRef {
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
	if !isValidRef(ref) {
		return nil
	}

	segments := strings.SplitN(atParts[0], "/", 3)
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		return nil
	}
	if !isValidSegment(segments[0]) || !isValidSegment(segments[1]) {
		return nil
	}

	parsed := &usesRef{
		Owner: segments[0],
		Repo:  segments[1],
		Ref:   ref,
		Raw:   uses,
	}
	if len(segments) == 3 {
		if !isValidPath(segments[2]) {
			return nil
		}
		parsed.Path = segments[2]
	}

	return parsed
}

// ReusableWorkflowRef is a parsed `uses:` reference to a reusable workflow
// file — the owner/repo/.github/workflows/<name>.yml@ref shape that
// ParseActionRef rejects. Path is the in-repo workflow file path.
//
// ParseReusableWorkflowRef is the only constructor; treat zero values as
// invalid.
type ReusableWorkflowRef struct {
	Owner string // e.g. "octo"
	Repo  string // e.g. "workflows"
	Path  string // e.g. ".github/workflows/release.yml"
	Ref   string // tag, branch, or full SHA as written after `@`
	Raw   string // original `uses:` string (post-trim)
}

// NWO returns owner/repo (Name With Owner). Zero-value refs return the
// empty string.
func (r ReusableWorkflowRef) NWO() string {
	if r.Owner == "" && r.Repo == "" {
		return ""
	}
	return r.Owner + "/" + r.Repo
}

// FullName returns owner/repo/path — the fully-qualified reusable workflow
// identity.
func (r ReusableWorkflowRef) FullName() string {
	return r.Owner + "/" + r.Repo + "/" + r.Path
}

// ParseReusableWorkflowRef parses the remote reusable-workflow `uses:` shape
// that ParseActionRef rejects: owner/repo/.github/workflows/<name>.yml@ref. It
// returns nil for anything else, including local reusable workflows
// (./.github/workflows/...) — use IsLocalReusableWorkflow for those — and
// nested paths, since reusable workflows must live directly under
// .github/workflows/.
func ParseReusableWorkflowRef(uses string) *ReusableWorkflowRef {
	parsed := splitUsesRef(uses)
	if parsed == nil {
		return nil
	}
	if !isReusableWorkflow(parsed.Path) {
		return nil
	}
	return &ReusableWorkflowRef{
		Owner: parsed.Owner,
		Repo:  parsed.Repo,
		Path:  parsed.Path,
		Ref:   parsed.Ref,
		Raw:   parsed.Raw,
	}
}

// isValidSegment enforces the GitHub character set (alphanumerics, hyphens,
// underscores, periods) for owner, repo, and path segments, keeping these
// values safe for use in URL paths and GraphQL string literals. The
// whole-segment "." and ".." are rejected. Do not replace with a regexp: this
// is on the hot path.
func isValidSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
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

// isValidRef gates the ref (the part after @): non-empty and no colons (which
// would be ambiguous with the legacy pin key separator).
func isValidRef(ref string) bool {
	if ref == "" {
		return false
	}
	return !strings.Contains(ref, ":")
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

// isValidPath validates the sub-directory path of a uses: reference, applying
// isValidSegment (GitHub charset, no "." / "..") to each segment.
func isValidPath(p string) bool {
	if p == "" {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if !isValidSegment(seg) {
			return false
		}
	}
	return true
}

// isWorkflowFile reports whether a uses: path points at a YAML file anywhere
// under .github/workflows/ (directly or nested). It anchors on the prefix
// rather than a substring so nested folders named .github/workflows/ elsewhere
// don't match. Intentionally broader than isReusableWorkflow.
func isWorkflowFile(path string) bool {
	if path == "" {
		return false
	}
	if !strings.HasPrefix(path, ".github/workflows/") {
		return false
	}
	return isYAMLFile(path)
}

// isReusableWorkflow reports whether a uses: path names a reusable workflow
// file: a single YAML file directly under .github/workflows/ (nothing nested).
// This is the strict classifier ParseReusableWorkflowRef accepts.
func isReusableWorkflow(path string) bool {
	const prefix = ".github/workflows/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	name := path[len(prefix):]
	if name == "" || strings.Contains(name, "/") {
		return false
	}
	return isYAMLFile(name)
}

// IsLocalReusableWorkflow reports whether a local `uses:` value (one starting
// with "./") names a reusable workflow file rather than a composite action
// directory. Pass the raw, untrimmed `uses:` string; the leading "./" must be
// present:
//
//   - "./.github/workflows/ci.yml"  → true  (local reusable workflow)
//   - "./my-composite-action"       → false (local composite action)
func IsLocalReusableWorkflow(localUses string) bool {
	return strings.HasPrefix(localUses, "./") && isYAMLFile(localUses)
}

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
// input whose owner/repo/path/ref components are unsafe to hand to the
// downstream URL/GraphQL builders (control characters, traversal tokens,
// or quote/whitespace metacharacters in the ref).
//
// The returned pointer is non-nil iff the input names a real repository
// action (composite or javascript) at owner/repo[/path]@ref.
func ParseActionRef(uses string) *ActionRef {
	parsed := splitUsesRef(uses)
	if parsed == nil {
		return nil
	}
	// Reject anything that lives under .github/workflows/ as a YAML file —
	// directly or nested. Nothing valid as a repository action lives there;
	// the direct-child form is a reusable workflow (use
	// ParseReusableWorkflowRef), and a nested form is malformed either way.
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

// usesRef is the unexported carrier for the components splitUsesRef extracts
// before classification. It deliberately is NOT ActionRef: a freshly split ref
// may turn out to be a reusable workflow, so handing back an ActionRef (whose
// contract is "a repository action") would be a lie until the caller has run
// the workflow-file check.
type usesRef struct {
	Owner string
	Repo  string
	Path  string
	Ref   string
	Raw   string
}

// splitUsesRef performs the parse shared by ParseActionRef and
// ParseReusableWorkflowRef: prefilter the input, split at the FIRST `@`
// (so a ref that itself contains `@` — e.g. a branch named "release@2024"
// in owner/repo/.github/workflows/ci.yml@release@2024 — keeps the whole
// "release@2024" as the ref), then validate the ref and the
// owner/repo[/path] segments against the security boundary.
//
// It returns the parsed components, or nil if the input is not a valid
// owner/repo[/path]@ref shape. It does NOT classify the result as action vs
// reusable workflow; that is left to the caller.
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
// ParseActionRef deliberately rejects. It carries the same components as
// ActionRef; Path is the in-repo workflow file path (e.g.
// ".github/workflows/release.yml"), and Ref is the full ref as written
// after the FIRST `@`, so a ref containing `@` survives intact.
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

// ParseReusableWorkflowRef parses the *remote* reusable-workflow `uses:` shape
// that ParseActionRef rejects: owner/repo/.github/workflows/<name>.yml@ref. It
// returns nil for anything that is not a remote reusable workflow — repository
// actions, expression refs, docker images, or any input whose components are
// unsafe for the downstream URL/GraphQL builders.
//
// It deliberately rejects LOCAL reusable workflows (./.github/workflows/...);
// those have no owner/repo and a different resolution path. Use
// IsLocalReusableWorkflow for that shape. It also rejects nested paths such as
// .github/workflows/sub/ci.yml: GitHub reusable workflows live directly under
// .github/workflows/, so only a single file segment is accepted.
//
// It is the mirror of ParseActionRef for the reusable shape, and shares the
// same first-`@` split and security validation. Downstream consumers that
// must derive a reusable workflow's repository and file path (e.g. to locate
// that repo's detached lockfile) should use this rather than hand-rolling the
// split: a naive last-`@` split mis-parses refs that contain `@`.
//
// The returned pointer is non-nil iff the input names a reusable workflow
// file at owner/repo/.github/workflows/<name>.{yml,yaml}@ref.
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

// isValidSegment enforces the GitHub character set for owner names, repository
// names, and action path segments. GitHub allows alphanumerics, hyphens,
// underscores, and periods; reject anything else to keep these values safe for
// use in URL paths and GraphQL string literals without per-call escaping bugs.
// The whole-segment values "." and ".." are rejected too — they aren't valid
// segments anyway.
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

// isValidRef gates the ref (the part after @). Only rejects empty refs and
// colons (which would be ambiguous with the legacy pin key separator).
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

// isValidPath validates the subdirectory path segment of a uses: reference.
// Each segment is validated by isValidSegment, which enforces the GitHub
// character set and rejects the "." / ".." traversal tokens. This extends the
// security boundary from owner/repo into the path component.
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
// under .github/workflows/ (directly or nested). ParseActionRef uses it to
// reject such paths wholesale: a repository action never lives there. The
// direct-child form is a reusable workflow; a nested form is malformed. This
// is intentionally broader than isReusableWorkflow.
//
// Anchor on prefix: substring matching would misclassify composite actions
// whose nested folder happens to contain that segment (e.g.
// tools/.github/workflows/).
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
// file: a single YAML file directly under .github/workflows/. GitHub reusable
// workflows live there and nowhere deeper, so a nested path like
// .github/workflows/sub/ci.yml is NOT a reusable workflow. This is the strict
// classifier ParseReusableWorkflowRef accepts; ParseActionRef rejects the
// broader isWorkflowFile set, so the two parsers never both accept an input.
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

// IsLocalReusableWorkflow reports whether a local `uses:` value (one that
// starts with "./") names a reusable workflow file rather than a composite
// action directory.
//
// Pass the raw, untrimmed `uses:` string from the workflow step — the leading
// "./" must be present:
//
//   - "./.github/workflows/ci.yml"  → true  (local reusable workflow)
//   - "./my-composite-action"       → false (local composite action)
//
// Call this only after confirming the `uses:` value has a "./" prefix; a
// value without "./" is a repository action or reusable workflow, not a
// local reference, and should be parsed with [ParseActionRef] or
// [ParseReusableWorkflowRef] instead.
//
// The distinction matters because composite action directories and reusable
// workflow files are resolved differently by the runner: workflow files are
// fetched from a specific checked-out path, while directories are run as
// composite actions.
func IsLocalReusableWorkflow(localUses string) bool {
	return strings.HasPrefix(localUses, "./") && isYAMLFile(localUses)
}

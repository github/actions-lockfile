package lockfile

import "strings"

// ActionRef is a parsed `uses:` reference to a repository action.
//
// ParseActionRef is the only constructor; treat zero values as invalid.
type ActionRef struct {
	Owner string // e.g. "actions"
	Repo  string // e.g. "checkout"
	Path  string // e.g. "save" for actions/cache/save@v4
	Ref   PinRef // tag/branch name or full SHA after `@`; see [ParsePinRef]
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

// ParseActionRef parses a `uses:` string into an ActionRef. It returns
// (nil, nil) for anything that is not a repository action — expression refs,
// local paths, docker images, reusable workflow files, or any input whose
// components are unsafe for the downstream URL/GraphQL builders. It returns
// (nil, *[RefError]) when the input has the shape of a repository action but
// the text after `@` fails [ParsePinRef]; callers must treat that as an error,
// not skip it.
func ParseActionRef(uses string) (*ActionRef, error) {
	parsed, err := splitUsesRef(uses)
	if parsed == nil {
		return nil, nil
	}
	// A YAML file under .github/workflows/ is never a repository action:
	// the direct-child form is a reusable workflow, anything nested is
	// malformed.
	if isWorkflowFile(parsed.Path) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ActionRef{
		Owner: parsed.Owner,
		Repo:  parsed.Repo,
		Path:  parsed.Path,
		Ref:   parsed.Ref,
		Raw:   parsed.Raw,
	}, nil
}

// usesRef carries the components splitUsesRef extracts before classification.
// It is deliberately not an ActionRef, since a freshly split ref may turn out
// to be a reusable workflow.
type usesRef struct {
	Owner string
	Repo  string
	Path  string
	Ref   PinRef
	Raw   string
}

// splitUsesRef performs the parse shared by ParseActionRef and
// ParseReusableWorkflowRef: prefilter the input, split at the FIRST `@` (so a
// ref containing `@`, e.g. a branch named "release@2024", stays intact), then
// validate the owner/repo[/path] segments and the ref. It does not classify
// the result as action vs reusable workflow.
//
// It returns nil for inputs that are not repository references. When the
// shape is valid but the ref fails ParsePinRef, it returns the shape (with a
// zero Ref) and the *RefError so callers can fail closed.
func splitUsesRef(uses string) (*usesRef, error) {
	uses = strings.TrimSpace(uses)

	if uses == "" || containsControlChars(uses) {
		return nil, nil
	}
	if strings.HasPrefix(uses, "./") {
		return nil, nil
	}
	if strings.HasPrefix(uses, "docker://") {
		return nil, nil
	}
	if strings.Contains(uses, "${") {
		return nil, nil
	}

	atParts := strings.SplitN(uses, "@", 2)
	if len(atParts) != 2 {
		return nil, nil
	}

	segments := strings.SplitN(atParts[0], "/", 3)
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		return nil, nil
	}
	if !isValidSegment(segments[0]) || !isValidSegment(segments[1]) {
		return nil, nil
	}

	parsed := &usesRef{
		Owner: segments[0],
		Repo:  segments[1],
		Raw:   uses,
	}
	if len(segments) == 3 {
		if !isValidPath(segments[2]) {
			return nil, nil
		}
		parsed.Path = segments[2]
	}

	pinRef, err := ParsePinRef(atParts[1])
	if err != nil {
		return parsed, err
	}
	parsed.Ref = pinRef
	return parsed, nil
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
	Ref   PinRef // tag/branch name or full SHA after `@`; see [ParsePinRef]
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
// .github/workflows/. It returns (nil, *[RefError]) when the input is a remote
// reusable workflow whose ref fails [ParsePinRef].
func ParseReusableWorkflowRef(uses string) (*ReusableWorkflowRef, error) {
	parsed, err := splitUsesRef(uses)
	if parsed == nil {
		return nil, nil
	}
	if !isReusableWorkflow(parsed.Path) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ReusableWorkflowRef{
		Owner: parsed.Owner,
		Repo:  parsed.Repo,
		Path:  parsed.Path,
		Ref:   parsed.Ref,
		Raw:   parsed.Raw,
	}, nil
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

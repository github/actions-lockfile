package lockfile

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrFutureVersion is the sentinel returned (via errors.Is) when Parse refuses
// a lockfile whose schema version is newer than this binary supports. External
// consumers (e.g. Dependabot) can detect this specific failure mode without
// scraping the error string.
var ErrFutureVersion = errors.New("lockfile version is newer than this binary supports")

// ParseError describes a failure to parse a dependency lockfile. It is always
// returned (via errors.As) by [Parse] rather than plain errors, so callers can
// print file:line:col diagnostics without scraping error strings.
//
// Line and Column, when non-zero, are the 1-indexed position within the
// lockfile bytes that the failure refers to. They index the lockfile file
// (.github/workflows/actions.lock), not any workflow .yml file.
//
// Column is set for semantic failures that Parse detects by walking the
// retained YAML tree (e.g. an unknown field, a duplicate pin key). It is zero
// for low-level YAML syntax errors where only a line number is available —
// a structurally malformed document has no node tree to resolve a column from.
//
// Msg is the human-readable description of the failure, without any position
// prefix. Use Error() to get the full "line N, column M: reason" string.
type ParseError struct {
	Line   int
	Column int
	Msg    string
	err    error
}

func (e *ParseError) Error() string {
	switch {
	case e.Line > 0 && e.Column > 0:
		return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Msg)
	case e.Line > 0:
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	default:
		return e.Msg
	}
}

func (e *ParseError) Unwrap() error {
	return e.err
}

// yamlLinePattern matches the 1-indexed position gopkg.in/yaml.v3 embeds in its
// error messages: "yaml: line N: ..." for syntax errors, or "  line N: ..."
// within an "unmarshal errors:" block for type errors.
var yamlLinePattern = regexp.MustCompile(`line (\d+):`)

// leadingYAMLPosition matches yaml.v3's "yaml:" package prefix and any
// immediately following "line N:" position.
var leadingYAMLPosition = regexp.MustCompile(`^yaml: (line \d+: )?`)

// newYAMLParseError converts a gopkg.in/yaml.v3 error into a ParseError,
// lifting the line number out of the message so consumers receive it as
// structured data instead of having to scrape the string themselves.
func newYAMLParseError(err error) *ParseError {
	msg := err.Error()
	line := 0
	if m := yamlLinePattern.FindStringSubmatch(msg); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			line = n
		}
	}
	return &ParseError{
		Line: line,
		Msg:  leadingYAMLPosition.ReplaceAllString(msg, ""),
		err:  err,
	}
}

// Version is the only supported lockfile schema version.
const Version = "v0.0.1"

// Path is the canonical repo-relative location of the dependency lockfile.
const Path = ".github/workflows/actions.lock"

// File is the parsed lockfile shape.
//
//	# .github/workflows/actions.lock
//	version: v0.0.1
//	workflows:
//	  .github/workflows/deploy.yml:
//	    - actions/checkout@v6:sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
//	dependencies:
//	  actions/checkout@v4.3.1:sha1-34e114876b0b11c390a56381ad16ebd13914f8d5:
//	    tag: v4.3.1
//	    branch: main
//	    commit: sha1-34e114876b0b11c390a56381ad16ebd13914f8d5
//	    owner_id: 44036562
//	    repo_id: 197814629
//	    uses:
//	      - actions/cache@v4.0.0:sha1-...
//
// The Go field `Dependencies` maps to the YAML key `dependencies:` — the
// lockfile's deduplicated action DAG. Each entry's `uses:` list names the
// action's direct nested dependencies, reusing the same canonical pin keys.
// Workflow entries hold the full transitive closure as a flat list of pin
// keys for cold readability.
type File struct {
	// Version is the lockfile schema version string (e.g. "v0.0.1"). It is
	// always equal to the [Version] constant for files Parse accepts.
	Version string `yaml:"version"`

	// Dependencies maps each canonical pin key (OWNER/REPO@REF:ALGO-HEX) to
	// the resolved [Action] metadata for that pin. The map is deduplicated:
	// multiple workflows that share an action produce a single entry here.
	// Use [File.LookupWorkflow] to find the pin keys for a specific workflow,
	// then index into this map to retrieve each action's metadata.
	Dependencies map[string]Action `yaml:"dependencies"`

	// Workflows maps each repo-relative workflow file path (e.g.
	// ".github/workflows/release.yml") to the flat, transitive list of
	// canonical pin keys that workflow depends on. Pin keys are in
	// OWNER/REPO@REF:ALGO-HEX form and serve as lookup keys into
	// Dependencies. Prefer [File.LookupWorkflow] over indexing this
	// map directly.
	Workflows map[string][]string `yaml:"workflows"`

	// node retains the parsed YAML tree so callers can resolve positions for
	// their own diagnostics via Position/KeyPosition. It is nil on the
	// zero-value File returned alongside an error. yaml.v3 ignores this
	// unexported field during decoding.
	node *yaml.Node
}

// Position returns the 1-indexed line and column of the value node reached by
// following path as a sequence of mapping keys from the lockfile root (e.g.
// Position("version") points at the version value). ok is false when the path
// can't be resolved or no node tree was retained.
func (f File) Position(path ...string) (line, col int, ok bool) {
	v := f.valueNode(path)
	if v == nil {
		return 0, 0, false
	}
	return v.Line, v.Column, true
}

// KeyPosition is like Position but resolves the position of the final path
// segment's *key* node rather than its value. It is the right anchor for map
// entries whose key is the meaningful token (e.g. a dependency pin key or a
// workflow path under "workflows").
func (f File) KeyPosition(path ...string) (line, col int, ok bool) {
	if len(path) == 0 {
		return 0, 0, false
	}
	m := docMapping(f.node)
	for _, key := range path[:len(path)-1] {
		_, v := mappingEntry(m, key)
		if v == nil {
			return 0, 0, false
		}
		m = v
	}
	k, _ := mappingEntry(m, path[len(path)-1])
	if k == nil {
		return 0, 0, false
	}
	return k.Line, k.Column, true
}

// valueNode walks path from the lockfile root mapping, returning the value
// node of the final segment, or nil when any segment is missing.
func (f File) valueNode(path []string) *yaml.Node {
	m := docMapping(f.node)
	var v *yaml.Node
	for _, key := range path {
		_, v = mappingEntry(m, key)
		if v == nil {
			return nil
		}
		m = v
	}
	return v
}

// docMapping unwraps a document node to its top-level mapping, returning nil
// when n is not a mapping (or is absent).
func docMapping(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// mappingEntry returns the key and value nodes for key within a mapping node,
// or (nil, nil) when m is not a mapping or the key is absent.
func mappingEntry(m *yaml.Node, key string) (k, v *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// LookupWorkflow returns the flat, transitive list of canonical pin keys for
// the given repo-relative workflow path (e.g. ".github/workflows/deploy.yml").
// The returned bool reports whether the workflow path was found in the lockfile.
//
// Each string in the returned slice is a canonical pin key in
// OWNER/REPO@REF:ALGO-HEX form. To retrieve the full action metadata for a
// pin, look it up in File.Dependencies:
//
//	pins, ok := f.LookupWorkflow(".github/workflows/deploy.yml")
//	for _, key := range pins {
//	    action := f.Dependencies[key]
//	    fmt.Println(action.Branch, action.Commit)
//	}
//
// A workflow that is present in the lockfile but has no dependencies returns
// an empty slice and ok=true. ok=false means the workflow path was never
// onboarded into the lockfile at all.
func (f File) LookupWorkflow(workflowKey string) ([]string, bool) {
	w, ok := f.Workflows[workflowKey]
	return w, ok
}

// Action carries the per-action metadata recorded in the lockfile under the
// pin key.
//
// Tag is the release/tag name at the pinned commit, if one exists. Optional:
// commits that are not tagged (e.g. pinned directly to a branch SHA) omit
// this field.
//
// Branch is a branch of the action's repository that contains the pinned
// commit. Required: Parse rejects an Action without one. A valid branch
// confirms that the commit exists in the expected repository — a SHA that
// isn't reachable from any branch in the source repo could belong to a fork
// or an attacker-supplied commit, which SHA-only pinning cannot detect.
//
// Commit holds the digest in algo-prefixed form (e.g. "sha1-abc123..." or
// "sha256-def456..."). This is the same digest that appears in the pin key.
// Required.
//
// OwnerID and RepoID are the GitHub numeric IDs for the action's repository
// owner and repository respectively. Consumers use them to detect if the
// action has been transferred to a new owner between lockfile regenerations —
// a repository transfer changes the owner name but not the owner ID.
//
// Uses lists the action's direct nested dependencies (composite action
// `uses:` steps) as canonical pin keys. Empty for leaf actions (node,
// docker); populated for composite actions.
type Action struct {
	Tag     string   `yaml:"tag,omitempty"`
	Branch  string   `yaml:"branch,omitempty"`
	Commit  string   `yaml:"commit,omitempty"`
	OwnerID int64    `yaml:"owner_id"`
	RepoID  int64    `yaml:"repo_id"`
	Uses    []string `yaml:"uses,omitempty"`
}

// Parse unmarshals the raw bytes of a lockfile and returns the parsed [File].
// Pass the contents of .github/workflows/actions.lock (available as the
// [Path] constant) or any other lockfile source.
//
// Parse checks structural validity — unknown top-level keys are rejected
// and required [Action] fields must be present — but does not verify pin
// integrity (e.g. that a SHA matches the ref) or that actions exist on
// GitHub. Those checks belong to the caller (e.g. the check command in
// gh-actions-lock).
//
// MaxParseSize is the maximum number of bytes Parse will accept. Inputs larger
// than this are rejected before any YAML parsing takes place to prevent
// memory-exhaustion DoS from oversized or yaml-bomb documents.
const MaxParseSize = 1 << 20 // 1 MiB
//
// # Optional paths parameter
//
// The variadic paths parameter is optional. Most callers should omit it.
//
//   - Omit paths (or pass nil) to validate every dependency entry in the
//     lockfile. This is the right choice for whole-file tooling: CLI
//     regeneration, Dependabot, schema linters.
//
//   - Pass one or more repo-relative workflow file paths (e.g.
//     ".github/workflows/deploy.yml") to limit field validation to only the
//     dependency entries referenced by those workflows. Entries outside the
//     requested set are still parsed and returned, but required-field checks
//     are skipped for them. This lets a single corrupt unrelated entry fail
//     without blocking the workflows you actually care about.
//
// A path that does not appear in the lockfile's workflows map silently
// contributes zero entries to validate — the lockfile is returned as-is for
// that path. This is intentional: a workflow not yet onboarded into the
// lockfile should not cause Parse to fail.
//
// # Canonicalization
//
// Action map keys and workflow dependency entries are lowercased via
// [ParsePin] so that lookups by [Pin.String] succeed regardless of the
// source casing of owner, repo, algorithm, or hex in the YAML. Entries
// that are not valid pin strings are preserved verbatim for caller
// diagnostics. Workflow path keys are NOT canonicalized — file paths are
// case-sensitive on Linux.
func Parse(contents []byte, paths ...string) (File, error) {
	if len(contents) > MaxParseSize {
		return File{}, &ParseError{
			Msg: fmt.Sprintf("lockfile too large: %d bytes (max %d)", len(contents), MaxParseSize),
		}
	}
	var root yaml.Node
	if err := yaml.Unmarshal(contents, &root); err != nil {
		return File{}, newYAMLParseError(err)
	}
	var f File
	if err := root.Decode(&f); err != nil {
		return File{}, newYAMLParseError(err)
	}
	// Retain the tree so semantic errors below (and consumers) can resolve
	// precise line+column positions within the lockfile.
	f.node = &root

	if f.Version == "" {
		// No version node to point at; anchor at the top of the document.
		pe := &ParseError{Msg: "dependency lockfile version is required"}
		if m := docMapping(f.node); m != nil {
			pe.Line, pe.Column = m.Line, m.Column
		}
		return File{}, pe
	}
	if f.Version != Version {
		msg := fmt.Sprintf("unsupported dependency lockfile version %q", f.Version)
		var wrapped error
		if isFutureVersion(f.Version, Version) {
			msg = fmt.Sprintf(
				"lockfile version %s is newer than this binary supports (%s); "+
					"upgrade the tool that reads this lockfile to a build that supports %s",
				f.Version, Version, f.Version,
			)
			wrapped = ErrFutureVersion
		}
		pe := &ParseError{Msg: msg, err: wrapped}
		if l, c, ok := f.Position("version"); ok {
			pe.Line, pe.Column = l, c
		}
		return File{}, pe
	}
	if pe := validateKnownFields(&f, paths); pe != nil {
		return File{}, pe
	}
	if pe := validateWorkflowPaths(&f); pe != nil {
		return File{}, pe
	}
	if conflictKey, err := canonicalizeActions(&f); err != nil {
		pe := &ParseError{Msg: err.Error(), err: err}
		if l, c, ok := f.KeyPosition("dependencies", conflictKey); ok {
			pe.Line, pe.Column = l, c
		}
		return File{}, pe
	}
	canonicalizeWorkflowDependencies(&f)
	if cycle, err := detectUsesCycle(&f); err != nil {
		pe := &ParseError{Msg: err.Error()}
		if l, c, ok := f.KeyPosition("dependencies", cycle); ok {
			pe.Line, pe.Column = l, c
		}
		return File{}, pe
	}
	return f, nil
}

// detectUsesCycle reports a cycle in the action uses graph using
// recursive DFS with three-colour marking. It returns the key of the node
// that forms the back-edge, or ("", nil) when the graph is acyclic.
//
// The recursion depth is bounded by the number of unique keys in
// f.Dependencies, which is itself bounded by MaxParseSize (a 1 MiB file
// can hold at most ~5,000 dependency entries). Go's default goroutine stack
// grows dynamically up to 1 GB, so 5,000 frames is well within budget.
//
// The runner rejects cycles at execution time via CompositeActionsMaxDepth
// (actions/runner: src/Runner.Common/Constants.cs). Detecting them at parse
// time shifts the failure left so consumers never receive a File whose uses
// graph is not a DAG.
func detectUsesCycle(f *File) (cycleKey string, err error) {
	const (
		white = 0 // unvisited
		grey  = 1 // on the current DFS stack
		black = 2 // fully processed
	)
	color := make(map[string]int, len(f.Dependencies))

	var visit func(key string) bool
	visit = func(key string) bool {
		if color[key] == grey {
			cycleKey = key
			return true
		}
		if color[key] == black {
			return false
		}
		color[key] = grey
		action, ok := f.Dependencies[key]
		if ok {
			for _, dep := range action.Uses {
				if visit(dep) {
					if cycleKey == "" {
						cycleKey = key
					}
					return true
				}
			}
		}
		color[key] = black
		return false
	}

	for key := range f.Dependencies {
		if color[key] == white {
			if visit(key) {
				return cycleKey, fmt.Errorf("uses cycle detected at dependency %q", cycleKey)
			}
		}
	}
	return "", nil
}

// validateWorkflowPaths checks that every key in f.Workflows is a safe
// repo-relative file path. Workflow keys are used by consumers as file paths
// (e.g. to open the workflow file on disk), so a crafted lockfile with a key
// like "../../../etc/passwd" or an absolute path "/etc/shadow" would give
// any consumer that calls os.Open(key) an arbitrary-read primitive.
//
// Rules:
//   - Must not be empty.
//   - Must not be an absolute path (no leading "/").
//   - Must not contain ".." as a path segment — rejects traversal after
//     path.Clean even if embedded in a longer path.
//   - Must not contain null bytes or other control characters.
func validateWorkflowPaths(f *File) *ParseError {
	_, workflowsNode := mappingEntry(docMapping(f.node), "workflows")
	for key := range f.Workflows {
		if err := checkWorkflowPathKey(key); err != nil {
			pe := &ParseError{Msg: err.Error()}
			if workflowsNode != nil {
				if k, _ := mappingEntry(workflowsNode, key); k != nil {
					pe.Line, pe.Column = k.Line, k.Column
				}
			}
			return pe
		}
	}
	return nil
}

func checkWorkflowPathKey(p string) error {
	if p == "" {
		return fmt.Errorf("workflow path key must not be empty")
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("workflow path key must be repo-relative, not absolute: %q", p)
	}
	for _, c := range p {
		if c <= 0x1F || c == 0x7F {
			return fmt.Errorf("workflow path key contains control characters: %q", p)
		}
		// Reject backslash and colon to prevent Windows-style absolute paths
		// (e.g. "C:\..." or UNC "\\server\...") and backslash-based traversal
		// (e.g. "..\\..\\.." ) from bypassing the forward-slash checks above.
		if c == '\\' || c == ':' {
			return fmt.Errorf("workflow path key contains invalid character %q: %q", string(c), p)
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("workflow path key contains path traversal: %q", p)
		}
	}
	return nil
}

// allowedFileKeys is the set of permitted top-level lockfile keys. It mirrors
// the document-level properties declared in lockfile-v0.0.1.json.
var allowedFileKeys = map[string]struct{}{
	"version":      {},
	"workflows":    {},
	"dependencies": {},
}

// allowedActionKeys is the set of permitted keys within a dependency's Action
// mapping. It mirrors the $defs/action properties in lockfile-v0.0.1.json.
var allowedActionKeys = map[string]struct{}{
	"tag":      {},
	"branch":   {},
	"commit":   {},
	"owner_id": {},
	"repo_id":  {},
	"uses":     {},
}

// requiredActionKeys lists the keys every dependency's Action mapping must
// carry, in report order. It mirrors the $defs/action "required" list in
// lockfile-v0.0.1.json. `tag` is optional (not every commit is a release) and
// `uses` is required only for composite actions — a condition the lockfile
// alone can't express — so neither appears here.
var requiredActionKeys = []string{"branch", "commit", "owner_id", "repo_id"}

// validateKnownFields enforces the schema's additionalProperties:false and
// required rules on the lockfile's fixed-shape mappings — the document root and
// each dependency's metadata block. A stray, misspelled, or missing key is a
// positioned parse error rather than a silently dropped or defaulted field,
// matching the stricter parsing the embedded schema describes. Map-valued
// sections (workflow paths, dependency pin keys) carry arbitrary data keys and
// are intentionally not constrained here.
//
// When paths is non-empty, per-dependency checks (unknown keys, required keys,
// zero-value rejection) are scoped to only the dependency entries referenced
// by the union of f.Workflows[p] for each requested path. This prevents a
// single corrupt entry from failing every workflow that shares the lockfile.
// When paths is empty, every dependency entry is validated.
func validateKnownFields(f *File, paths []string) *ParseError {
	root := docMapping(f.node)
	if root == nil {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if _, ok := allowedFileKeys[k.Value]; !ok {
			return &ParseError{Line: k.Line, Column: k.Column, Msg: fmt.Sprintf("unknown lockfile field %q", k.Value)}
		}
	}
	_, deps := mappingEntry(root, "dependencies")
	if deps == nil || deps.Kind != yaml.MappingNode {
		return nil
	}

	// Build the in-scope set from the raw (pre-canonicalization) workflow
	// entries. nil means "validate all" (len(paths)==0); a non-nil but empty
	// map means "validate nothing" (requested paths had no matching deps).
	var inScope map[string]struct{}
	if len(paths) > 0 {
		inScope = make(map[string]struct{})
		for _, p := range paths {
			for _, pin := range f.Workflows[p] {
				inScope[pin] = struct{}{}
			}
		}
	}

	for i := 0; i+1 < len(deps.Content); i += 2 {
		pinKey := deps.Content[i]
		action := deps.Content[i+1]
		if action.Kind != yaml.MappingNode {
			continue
		}

		if inScope != nil {
			if _, ok := inScope[pinKey.Value]; !ok {
				continue
			}
		}

		present := make(map[string]struct{}, len(action.Content)/2)
		for j := 0; j+1 < len(action.Content); j += 2 {
			ak := action.Content[j]
			if _, ok := allowedActionKeys[ak.Value]; !ok {
				return &ParseError{
					Line:   ak.Line,
					Column: ak.Column,
					Msg:    fmt.Sprintf("unknown action field %q for dependency %q", ak.Value, pinKey.Value),
				}
			}
			present[ak.Value] = struct{}{}
		}
		for _, req := range requiredActionKeys {
			if _, ok := present[req]; !ok {
				// The missing key has no node to point at; anchor the error on
				// the dependency's pin key so callers can locate the entry.
				return &ParseError{
					Line:   pinKey.Line,
					Column: pinKey.Column,
					Msg:    fmt.Sprintf("missing required action field %q for dependency %q", req, pinKey.Value),
				}
			}
		}
		// Enforce non-zero values for fields where the zero value is
		// meaningless and would silently disable a security check.
		if pe := rejectZeroValues(action, pinKey.Value); pe != nil {
			return pe
		}
	}
	return nil
}

// nonEmptyStringKeys lists action fields that must be non-empty strings.
var nonEmptyStringKeys = map[string]struct{}{
	"branch": {},
	"commit": {},
}

// positiveIntKeys lists action fields that must be positive integers (> 0).
var positiveIntKeys = map[string]struct{}{
	"owner_id": {},
	"repo_id":  {},
}

// rejectZeroValues checks that required action fields carry meaningful values:
// string fields like "branch" must be non-empty, integer ID fields like
// "owner_id" and "repo_id" must be positive, the "commit" field must be
// a valid algo-hex digest string, and the "branch"/"tag" fields must not
// contain characters that are unsafe for downstream interpolation. A
// present-but-zero-value or injection-bearing field would silently disable
// the security check it's meant to enforce, or arm a downstream injection.
func rejectZeroValues(action *yaml.Node, dep string) *ParseError {
	for j := 0; j+1 < len(action.Content); j += 2 {
		key := action.Content[j]
		val := action.Content[j+1]

		if _, ok := nonEmptyStringKeys[key.Value]; ok {
			if val.Value == "" {
				return &ParseError{
					Line:   val.Line,
					Column: val.Column,
					Msg:    fmt.Sprintf("action field %q must not be empty for dependency %q", key.Value, dep),
				}
			}
		}

		if key.Value == "commit" && val.Value != "" {
			if !isValidAlgoHex(val.Value) {
				return &ParseError{
					Line:   val.Line,
					Column: val.Column,
					Msg:    fmt.Sprintf("action field \"commit\" must be an algo-hex digest (e.g. \"sha1-abc...\") for dependency %q, got %q", dep, val.Value),
				}
			}
		}

		// branch and tag values are used in GraphQL queries, log output,
		// and sometimes shell commands by consumers. Validate them with
		// the same denylist that ParseActionRef applies to refs so that a
		// crafted lockfile cannot arm a downstream injection through these
		// fields.
		if (key.Value == "branch" || key.Value == "tag") && val.Value != "" {
			if !isValidRef(val.Value) {
				return &ParseError{
					Line:   val.Line,
					Column: val.Column,
					Msg:    fmt.Sprintf("action field %q contains unsafe characters for dependency %q: %q", key.Value, dep, val.Value),
				}
			}
		}

		if _, ok := positiveIntKeys[key.Value]; ok {
			n, err := strconv.ParseInt(val.Value, 10, 64)
			if err != nil || n <= 0 {
				return &ParseError{
					Line:   val.Line,
					Column: val.Column,
					Msg:    fmt.Sprintf("action field %q must be a positive integer for dependency %q", key.Value, dep),
				}
			}
		}
	}
	return nil
}

// canonicalizeActions rewrites the Dependencies map so every key is the
// canonical form of its pin (Pin.String()). A conflict between two
// different source casings of the same pin is a parse error — the file
// would be ambiguous about which Action metadata applies. On conflict it
// returns the offending source key so callers can locate it in the YAML tree.
func canonicalizeActions(f *File) (string, error) {
	if len(f.Dependencies) == 0 {
		return "", nil
	}
	out := make(map[string]Action, len(f.Dependencies))
	for key, action := range f.Dependencies {
		canonical := key
		if pin, ok := ParsePin(key); ok {
			canonical = pin.String()
			// The commit field in the action body must match the digest
			// embedded in the pin key. A mismatch is a trust-confusion
			// attack: the caller looks up the action by key (and its
			// embedded hash) but the body carries a different hash that
			// a different downstream check might trust.
			pinDigest := pin.Algo + "-" + pin.Hex
			if action.Commit != "" && action.Commit != pinDigest {
				return key, fmt.Errorf(
					"action %q commit field %q disagrees with pin key digest %q",
					canonical, action.Commit, pinDigest,
				)
			}
		}
		// Canonicalize Uses entries too so cross-references resolve.
		if len(action.Uses) > 0 {
			canonUses := make([]string, len(action.Uses))
			for i, u := range action.Uses {
				if pin, ok := ParsePin(u); ok {
					canonUses[i] = pin.String()
				} else {
					canonUses[i] = u
				}
			}
			action.Uses = canonUses
		}
		if existing, dup := out[canonical]; dup {
			if !equalAction(existing, action) {
				return key, fmt.Errorf("duplicate action key %q after canonicalization with differing metadata", canonical)
			}
			continue
		}
		out[canonical] = action
	}
	f.Dependencies = out
	return "", nil
}

func equalAction(a, b Action) bool {
	if a.Tag != b.Tag || a.Branch != b.Branch || a.Commit != b.Commit ||
		a.OwnerID != b.OwnerID || a.RepoID != b.RepoID {
		return false
	}
	if len(a.Uses) != len(b.Uses) {
		return false
	}
	for i := range a.Uses {
		if a.Uses[i] != b.Uses[i] {
			return false
		}
	}
	return true
}

// canonicalizeWorkflowDependencies rewrites every workflow's pin list to
// canonical pin strings (Pin.String()) so lookups into the Dependencies map are
// casing-agnostic. Unparseable entries are preserved verbatim for downstream
// diagnostics to flag.
func canonicalizeWorkflowDependencies(f *File) {
	for path, deps := range f.Workflows {
		if len(deps) == 0 {
			continue
		}
		canonicalized := make([]string, len(deps))
		for i, dep := range deps {
			if pin, ok := ParsePin(dep); ok {
				canonicalized[i] = pin.String()
			} else {
				canonicalized[i] = dep
			}
		}
		f.Workflows[path] = canonicalized
	}
}

// schemaVersionRE matches "vMAJOR.MINOR.PATCH" with an optional leading "v"
// and no pre-release suffix. The lockfile schema version is a strict
// dotted-triple — anything else is unknown rather than future.
var schemaVersionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// isFutureVersion reports whether actual is a well-formed schema version
// strictly greater than supported. Used to distinguish "newer binary needed"
// (friendly upgrade path) from "garbage/unknown version" (generic refusal).
func isFutureVersion(actual, supported string) bool {
	a, ok := parseSchemaVersion(actual)
	if !ok {
		return false
	}
	s, ok := parseSchemaVersion(supported)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if a[i] != s[i] {
			return a[i] > s[i]
		}
	}
	return false
}

func parseSchemaVersion(v string) ([3]int, bool) {
	m := schemaVersionRE.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// isValidAlgoHex reports whether s is a properly-formed algo-hex digest string
// in the lockfile's "algo-hexdigest" format (e.g. "sha1-abc123..." or
// "sha256-abc123..."). It delegates hex and length validation to isValidDigest
// so the two never drift apart.
func isValidAlgoHex(s string) bool {
	dashIdx := strings.IndexByte(s, '-')
	if dashIdx <= 0 || dashIdx == len(s)-1 {
		return false
	}
	algo := strings.ToLower(s[:dashIdx])
	hex := strings.ToLower(s[dashIdx+1:])
	return isValidDigest(algo, hex)
}

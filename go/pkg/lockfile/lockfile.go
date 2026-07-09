package lockfile

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrFutureVersion is returned (via errors.Is) when Parse refuses a lockfile
// whose schema version is newer than this binary supports.
var ErrFutureVersion = errors.New("lockfile version is newer than this binary supports")

// ParseError describes a failure to parse a lockfile. [Parse] always returns
// it (via errors.As) so callers can print file:line:col diagnostics.
//
// Line and Column, when non-zero, are the 1-indexed position within the
// lockfile bytes. Column is zero for low-level YAML syntax errors, where only
// a line number is available. Msg is the description without any position
// prefix; use Error for the full "line N, column M: reason" string.
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

// yamlLinePattern matches the 1-indexed line gopkg.in/yaml.v3 embeds in its
// error messages ("yaml: line N: ..." or "  line N: ...").
var yamlLinePattern = regexp.MustCompile(`line (\d+):`)

// leadingYAMLPosition matches yaml.v3's "yaml:" prefix and any following
// "line N:" position.
var leadingYAMLPosition = regexp.MustCompile(`^yaml: (line \d+: )?`)

// newYAMLParseError converts a gopkg.in/yaml.v3 error into a ParseError,
// lifting the line number out of the message into structured data.
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

// Version is the latest lockfile schema version this binary writes.
const Version = "v0.0.2"

// Path is the canonical repo-relative location of the dependency lockfile.
const Path = ".github/workflows/actions.lock"

// CLIName is the canonical name of the CLI extension that manages lockfiles.
const CLIName = "gh actions-lock"

// File is the parsed lockfile shape.
//
//	# .github/workflows/actions.lock
//	version: v0.0.1
//	workflows:
//	  .github/workflows/deploy.yml:
//	    - actions/checkout@v6
//	dependencies:
//	  actions/checkout@v4.3.1:
//	    ref: v4.3.1
//	    commit: sha1-34e114876b0b11c390a56381ad16ebd13914f8d5
//	    owner_id: 44036562
//	    repo_id: 197814629
//	    uses:
//	      - actions/cache@v4.0.0
//
// Dependencies is the deduplicated action DAG; each entry's uses: list names
// its direct nested dependencies as canonical pin keys. Workflows holds each
// workflow's full transitive closure as a flat list of pin keys.
type File struct {
	// Version is the lockfile schema version string (e.g. "v0.0.1"), always
	// equal to the [Version] constant for files Parse accepts.
	Version string `yaml:"version"`

	// Dependencies maps each canonical pin key (OWNER/REPO@REF) to the
	// resolved [Action] metadata. Deduplicated across workflows. Use
	// [File.LookupWorkflow] to find a workflow's pin keys, then index here.
	Dependencies map[string]Action `yaml:"dependencies"`

	// Workflows maps each repo-relative workflow path to the flat, transitive
	// list of canonical pin keys (OWNER/REPO@REF) it depends on. Prefer
	// [File.LookupWorkflow] over indexing directly.
	Workflows map[string][]string `yaml:"workflows"`

	// node retains the parsed YAML tree so callers can resolve positions via
	// Position/KeyPosition. Nil on the zero-value File returned with an error.
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
// segment's key node rather than its value.
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

// LookupWorkflow returns the flat, transitive list of canonical pin keys
// (OWNER/REPO@REF) for the given repo-relative workflow path. Look each key up
// in File.Dependencies for its [Action] metadata:
//
//	pins, ok := f.LookupWorkflow(".github/workflows/deploy.yml")
//	for _, key := range pins {
//	    action := f.Dependencies[key]
//	    fmt.Println(action.Ref, action.Commit)
//	}
//
// ok=false means the workflow was never onboarded into the lockfile; an
// onboarded workflow with no dependencies returns an empty slice and ok=true.
func (f File) LookupWorkflow(workflowKey string) ([]string, bool) {
	w, ok := f.Workflows[workflowKey]
	return w, ok
}

// Action carries the per-action metadata recorded under a pin key.
//
// Ref is the git ref the commit was resolved from (required). Commit is the
// digest in algo-prefixed form (e.g. "sha1-abc123...", "sha256-def456..."),
// matching the digest in the pin key (required). OwnerID and RepoID are the
// GitHub numeric IDs for the owner and repository, used to detect a repository
// transfer (the name changes but the ID does not). Uses lists the action's
// direct nested dependencies as canonical pin keys — empty for leaf actions,
// populated for composite actions.
type Action struct {
	Ref     string   `yaml:"ref,omitempty"`
	Commit  string   `yaml:"commit,omitempty"`
	OwnerID int64    `yaml:"owner_id"`
	RepoID  int64    `yaml:"repo_id"`
	Uses    []string `yaml:"uses,omitempty"`
}

// MaxParseSize is the maximum number of bytes Parse accepts. Larger inputs are
// rejected before any YAML parsing to prevent memory-exhaustion DoS.
const MaxParseSize = 1 << 20 // 1 MiB

// Parse unmarshals the raw bytes of a lockfile and returns the parsed [File].
// Pass the contents of .github/workflows/actions.lock (the [Path] constant).
//
// Parse checks structural validity — unknown top-level keys are rejected and
// required [Action] fields must be present — but does not verify pin integrity
// or that actions exist on GitHub; those checks belong to the caller.
//
// The variadic paths parameter is optional. Omit it (or pass nil) to validate
// every dependency entry — the right choice for whole-file tooling. Pass one
// or more repo-relative workflow paths to limit required-field validation to
// the entries those workflows reference; other entries are still parsed and
// returned, and paths absent from the workflows map contribute nothing.
//
// Dependency keys and workflow entries are canonicalized (lowercased) via
// [ParsePin] so lookups by [Pin.String] are casing-agnostic. Workflow path
// keys are not canonicalized — file paths are case-sensitive.
func Parse(contents []byte, paths ...string) (File, error) {
	return parseInternal(contents, nil, paths)
}

// detectUsesCycle reports a cycle in the action uses graph via three-colour
// DFS, returning the key of the node that forms the back-edge, or ("", nil)
// when the graph is acyclic. Detecting cycles at parse time ensures consumers
// never receive a File whose uses graph is not a DAG.
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
// repo-relative file path. Consumers open these keys as files, so a crafted
// key like "../../../etc/passwd" or "/etc/shadow" would be an arbitrary-read
// primitive.
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
		// Reject backslash and colon to block Windows-style absolute paths
		// and backslash-based traversal from bypassing the checks above.
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

// allowedFileKeys is the set of permitted top-level lockfile keys.
var allowedFileKeys = map[string]struct{}{
	"version":      {},
	"workflows":    {},
	"dependencies": {},
}

// allowedActionKeys is the set of permitted keys within a v0.0.2 dependency's
// Action mapping.
var allowedActionKeys = map[string]struct{}{
	"ref":      {},
	"commit":   {},
	"owner_id": {},
	"repo_id":  {},
	"uses":     {},
}

// requiredActionKeys lists the keys every v0.0.2 dependency's Action mapping
// must carry, in report order.
var requiredActionKeys = []string{"ref", "commit", "owner_id", "repo_id"}

// nonEmptyStringKeys lists action fields that must be non-empty strings.
var nonEmptyStringKeys = map[string]struct{}{
	"ref":    {},
	"commit": {},
}

// positiveIntKeys lists action fields that must be positive integers (> 0).
var positiveIntKeys = map[string]struct{}{
	"owner_id": {},
	"repo_id":  {},
}

// rejectZeroValues checks that required action fields carry meaningful values:
// commit must be a valid algo-hex digest, ID fields must be positive, and
// nonEmptyStringKeys must not be blank. A present-but-zero value would silently
// disable the security check it enforces.
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

// rejectKeyRefMismatch returns a ParseError when the dependency key parses as
// a valid pin and the body's ref: field disagrees with the key's ref
// component, signalling an inconsistent hand-edit.
func rejectKeyRefMismatch(pinKey, action *yaml.Node) *ParseError {
	pin, ok := ParsePin(pinKey.Value)
	if !ok {
		return nil
	}
	for j := 0; j+1 < len(action.Content); j += 2 {
		key := action.Content[j]
		val := action.Content[j+1]
		if key.Value == "ref" && val.Value != "" && val.Value != pin.Ref {
			// When the pin key ref is a full SHA, the body's ref is the
			// discovered symbolic ref (tag/branch) — mismatch is expected.
			if IsFullSha(pin.Ref) {
				continue
			}
			return &ParseError{
				Line:   val.Line,
				Column: val.Column,
				Msg:    fmt.Sprintf("action body ref %q does not match pin key ref %q for dependency %q", val.Value, pin.Ref, pinKey.Value),
			}
		}
	}
	return nil
}

// rejectFullSHACommitMismatch returns a ParseError when the pin key's ref is a
// full commit SHA and the body's commit: field encodes a different digest.
// Full-SHA refs are immutable, so the commit must agree with the ref.
func rejectFullSHACommitMismatch(pinKey, action *yaml.Node) *ParseError {
	pin, ok := ParsePin(pinKey.Value)
	if !ok {
		return nil
	}
	if !IsFullSha(pin.Ref) {
		return nil
	}
	for j := 0; j+1 < len(action.Content); j += 2 {
		key := action.Content[j]
		val := action.Content[j+1]
		if key.Value == "commit" && val.Value != "" {
			// Extract the hex portion after the algo prefix (e.g. "sha1-<hex>").
			dashIdx := strings.IndexByte(val.Value, '-')
			if dashIdx <= 0 || dashIdx == len(val.Value)-1 {
				return nil // malformed commit caught elsewhere
			}
			commitHex := strings.ToLower(val.Value[dashIdx+1:])
			if strings.ToLower(pin.Ref) != commitHex {
				return &ParseError{
					Line:   val.Line,
					Column: val.Column,
					Msg:    fmt.Sprintf("pin key ref is a full SHA (%s) but commit digest %q does not match for dependency %q", ShortSHA(pin.Ref), val.Value, pinKey.Value),
				}
			}
		}
	}
	return nil
}

// canonicalizeActions rewrites the Dependencies map so every key is its pin's
// canonical form (Pin.String). Keys and Uses entries that don't parse as valid
// v0.0.2 pins are rejected. A conflict between two casings of the same pin is a
// parse error; the offending source key is returned so callers can locate it.
func canonicalizeActions(f *File) (string, error) {
	if len(f.Dependencies) == 0 {
		return "", nil
	}
	out := make(map[string]Action, len(f.Dependencies))
	for key, action := range f.Dependencies {
		pin, ok := ParsePin(key)
		if !ok {
			return key, fmt.Errorf("dependency key %q is not a valid pin (expected OWNER/REPO@REF)", key)
		}
		canonical := pin.String()
		// Canonicalize Uses entries too so cross-references resolve.
		if len(action.Uses) > 0 {
			canonUses := make([]string, len(action.Uses))
			for i, u := range action.Uses {
				uPin, uOk := ParsePin(u)
				if !uOk {
					return key, fmt.Errorf("uses entry %q in dependency %q is not a valid pin (expected OWNER/REPO@REF)", u, key)
				}
				canonUses[i] = uPin.String()
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
	if a.Ref != b.Ref || a.Commit != b.Commit ||
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
// canonical pin strings (Pin.String) so lookups into Dependencies are
// casing-agnostic. Entries that don't parse as valid v0.0.2 pins are rejected.
func canonicalizeWorkflowDependencies(f *File) (string, string, error) {
	for path, deps := range f.Workflows {
		if len(deps) == 0 {
			continue
		}
		canonicalized := make([]string, len(deps))
		for i, dep := range deps {
			pin, ok := ParsePin(dep)
			if !ok {
				return path, dep, fmt.Errorf("workflow %q dependency %q is not a valid pin (expected OWNER/REPO@REF)", path, dep)
			}
			canonicalized[i] = pin.String()
		}
		f.Workflows[path] = canonicalized
	}
	return "", "", nil
}

// schemaVersionRE matches "vMAJOR.MINOR.PATCH" with an optional leading "v"
// and no pre-release suffix.
var schemaVersionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// isFutureVersion reports whether actual is a well-formed schema version
// strictly greater than supported, distinguishing "newer binary needed" from
// "garbage/unknown version".
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

// isValidAlgoHex reports whether s is a well-formed algo-hex digest (e.g.
// "sha1-abc123...", "sha256-abc123..."), delegating hex and length checks to
// isValidDigest.
func isValidAlgoHex(s string) bool {
	dashIdx := strings.IndexByte(s, '-')
	if dashIdx <= 0 || dashIdx == len(s)-1 {
		return false
	}
	algo := strings.ToLower(s[:dashIdx])
	hex := strings.ToLower(s[dashIdx+1:])
	return isValidDigest(algo, hex)
}

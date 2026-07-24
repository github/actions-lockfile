package lockfile

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// supportedVersions lists all schema versions this binary can parse,
// ordered from oldest to newest.
var supportedVersions = []string{"v0.0.1", "v0.0.2"}

// ErrUnsupportedVersion is the sentinel returned when ParseWithPolicy refuses a
// lockfile whose version is older than the consumer's minimum.
var ErrUnsupportedVersion = fmt.Errorf("lockfile version is older than this consumer supports")

// VersionPolicy controls which lockfile schema versions a consumer accepts.
// Servers set their own policy to control the rollout of new formats.
type VersionPolicy struct {
	// Min is the oldest version the consumer can read. Lockfiles older than
	// this are rejected with ErrUnsupportedVersion.
	Min string
	// Max is the newest version the consumer can read. Lockfiles newer than
	// this are rejected with ErrFutureVersion.
	Max string
}

// DefaultPolicy returns a policy that accepts all versions this binary can
// parse — from the oldest supported through the latest.
func DefaultPolicy() VersionPolicy {
	return VersionPolicy{
		Min: supportedVersions[0],
		Max: supportedVersions[len(supportedVersions)-1],
	}
}

// ParseWithPolicy is like Parse but enforces version bounds. The lockfile's
// declared version must be within [policy.Min, policy.Max] inclusive.
func ParseWithPolicy(contents []byte, policy VersionPolicy, paths ...string) (File, error) {
	return parseInternal(contents, &policy, paths)
}

// parseInternal is the shared implementation for Parse and ParseWithPolicy.
// When policy is nil, DefaultPolicy() is used.
func parseInternal(contents []byte, policy *VersionPolicy, paths []string) (File, error) {
	if len(contents) > MaxParseSize {
		return File{}, &ParseError{
			Msg: fmt.Sprintf("lockfile too large: %d bytes (max %d)", len(contents), MaxParseSize),
		}
	}
	var root yaml.Node
	if err := yaml.Unmarshal(contents, &root); err != nil {
		return File{}, newYAMLParseError(err)
	}
	// yaml.v3's Decode rejects duplicate mapping keys, but with a generic
	// message. Detect duplicate dependency keys first so we can return a
	// domain-specific, positioned error before the generic decode error fires.
	if pe := rejectDuplicateDependencyKeys(&root); pe != nil {
		return File{}, pe
	}
	var f File
	if err := root.Decode(&f); err != nil {
		return File{}, newYAMLParseError(err)
	}
	f.node = &root

	if f.Version == "" {
		pe := &ParseError{Msg: "dependency lockfile version is required"}
		if m := docMapping(f.node); m != nil {
			pe.Line, pe.Column = m.Line, m.Column
		}
		return File{}, pe
	}

	p := DefaultPolicy()
	if policy != nil {
		p = *policy
	}
	if err := checkVersionPolicy(f.Version, p, f.node); err != nil {
		return File{}, err
	}

	if pe := validateKnownFieldsVersioned(&f, paths, f.Version); pe != nil {
		return File{}, pe
	}
	if pe := validateWorkflowPaths(&f); pe != nil {
		return File{}, pe
	}

	// For v0.0.1 files, migrate branch/tag → ref and canonicalize legacy pin keys.
	if f.Version == "v0.0.1" {
		migrateV001Actions(&f)
		if conflictKey, err := canonicalizeActionsV001(&f); err != nil {
			pe := &ParseError{Msg: err.Error(), err: err}
			if l, c, ok := f.KeyPosition("dependencies", conflictKey); ok {
				pe.Line, pe.Column = l, c
			}
			return File{}, pe
		}
		if wfPath, _, err := canonicalizeWorkflowDepsV001(&f); err != nil {
			pe := &ParseError{Msg: err.Error(), err: err}
			if l, c, ok := f.KeyPosition("workflows", wfPath); ok {
				pe.Line, pe.Column = l, c
			}
			return File{}, pe
		}
	} else {
		if conflictKey, err := canonicalizeActions(&f); err != nil {
			pe := &ParseError{Msg: err.Error(), err: err}
			if l, c, ok := f.KeyPosition("dependencies", conflictKey); ok {
				pe.Line, pe.Column = l, c
			}
			return File{}, pe
		}
		if wfPath, _, err := canonicalizeWorkflowDependencies(&f); err != nil {
			pe := &ParseError{Msg: err.Error(), err: err}
			if l, c, ok := f.KeyPosition("workflows", wfPath); ok {
				pe.Line, pe.Column = l, c
			}
			return File{}, pe
		}
	}

	if cycle, err := detectUsesCycle(&f); err != nil {
		pe := &ParseError{Msg: err.Error()}
		if l, c, ok := f.KeyPosition("dependencies", cycle); ok {
			pe.Line, pe.Column = l, c
		}
		return File{}, pe
	}

	// Normalize to the latest version: File always represents the current
	// schema regardless of what was on disk.
	f.Version = Version

	return f, nil
}

// checkVersionPolicy validates the lockfile version against the policy bounds.
func checkVersionPolicy(version string, policy VersionPolicy, node *yaml.Node) error {
	if !isKnownVersion(version) {
		// Unknown version — is it a future version?
		if isFutureVersion(version, policy.Max) {
			msg := fmt.Sprintf(
				"lockfile version %s is newer than this binary supports (%s); "+
					"upgrade the tool that reads this lockfile to a build that supports %s",
				version, policy.Max, version,
			)
			pe := &ParseError{Msg: msg, err: ErrFutureVersion}
			if l, c, ok := positionFromNode(node, "version"); ok {
				pe.Line, pe.Column = l, c
			}
			return pe
		}
		msg := fmt.Sprintf("unsupported dependency lockfile version %q", version)
		pe := &ParseError{Msg: msg}
		if l, c, ok := positionFromNode(node, "version"); ok {
			pe.Line, pe.Column = l, c
		}
		return pe
	}

	// Check against policy min.
	if versionLessThan(version, policy.Min) {
		msg := fmt.Sprintf(
			"lockfile version %s is older than this consumer supports (minimum %s); "+
				"regenerate the lockfile with a newer CLI",
			version, policy.Min,
		)
		pe := &ParseError{Msg: msg, err: ErrUnsupportedVersion}
		if l, c, ok := positionFromNode(node, "version"); ok {
			pe.Line, pe.Column = l, c
		}
		return pe
	}

	// Check against policy max.
	if versionLessThan(policy.Max, version) {
		msg := fmt.Sprintf(
			"lockfile version %s is newer than this consumer supports (maximum %s); "+
				"upgrade the tool that reads this lockfile",
			version, policy.Max,
		)
		pe := &ParseError{Msg: msg, err: ErrFutureVersion}
		if l, c, ok := positionFromNode(node, "version"); ok {
			pe.Line, pe.Column = l, c
		}
		return pe
	}

	return nil
}

func isKnownVersion(v string) bool {
	for _, sv := range supportedVersions {
		if sv == v {
			return true
		}
	}
	return false
}

// versionLessThan reports whether a < b using semver comparison.
func versionLessThan(a, b string) bool {
	return isFutureVersion(b, a)
}

func positionFromNode(node *yaml.Node, key string) (line, col int, ok bool) {
	m := docMapping(node)
	if m == nil {
		return 0, 0, false
	}
	_, v := mappingEntry(m, key)
	if v == nil {
		return 0, 0, false
	}
	return v.Line, v.Column, true
}

// ── v0.0.1 compat layer ─────────────────────────────────────────────────────

// allowedActionKeysV001 extends the v0.0.2 set with the legacy branch/tag fields.
var allowedActionKeysV001 = map[string]struct{}{
	"tag":      {},
	"branch":   {},
	"commit":   {},
	"owner_id": {},
	"repo_id":  {},
	"uses":     {},
}

// requiredActionKeysV001 — v0.0.1 did not require ref (it used tag/branch).
var requiredActionKeysV001 = []string{"commit", "owner_id", "repo_id"}

// migrateV001Actions walks the YAML node tree for a v0.0.1 lockfile and
// populates Action.Ref from the tag/branch fields using BestRef.
func migrateV001Actions(f *File) {
	root := docMapping(f.node)
	if root == nil {
		return
	}
	_, deps := mappingEntry(root, "dependencies")
	if deps == nil || deps.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(deps.Content); i += 2 {
		pinKey := deps.Content[i]
		actionNode := deps.Content[i+1]
		if actionNode.Kind != yaml.MappingNode {
			continue
		}

		var tag, branch string
		for j := 0; j+1 < len(actionNode.Content); j += 2 {
			k := actionNode.Content[j]
			v := actionNode.Content[j+1]
			switch k.Value {
			case "tag":
				tag = v.Value
			case "branch":
				branch = v.Value
			}
		}

		ref := BestRef(tag, branch)
		if ref == "" {
			// Fall back to extracting ref from the pin key.
			if pin, ok := parsePinV001(pinKey.Value); ok {
				ref = pin.Ref
			}
		}

		key := pinKey.Value
		if a, ok := f.Dependencies[key]; ok {
			if a.Ref == "" {
				a.Ref = ref
				f.Dependencies[key] = a
			}
		}
	}
}

// parsePinV001 parses a v0.0.1 pin key: "OWNER/REPO@REF:ALGO-HEX" or the
// simpler "OWNER/REPO@REF" form (some v0.0.1 lockfiles used tags without
// the digest suffix in certain contexts).
func parsePinV001(s string) (Pin, bool) {
	atIdx := strings.IndexByte(s, '@')
	if atIdx <= 0 || atIdx == len(s)-1 {
		return Pin{}, false
	}
	repoPath := s[:atIdx]
	refAndMaybeSuffix := s[atIdx+1:]

	if strings.Count(repoPath, "/") != 1 {
		return Pin{}, false
	}
	owner, repo, ok := SplitNWO(repoPath)
	if !ok {
		return Pin{}, false
	}

	// Strip the optional :algo-hex suffix.
	ref := refAndMaybeSuffix
	if colonIdx := strings.LastIndexByte(refAndMaybeSuffix, ':'); colonIdx > 0 {
		// Only strip if the part after the colon looks like algo-hex.
		possibleDigest := refAndMaybeSuffix[colonIdx+1:]
		possibleRef := refAndMaybeSuffix[:colonIdx]
		if looksLikeAlgoHex(possibleDigest) {
			ref = possibleRef
		}
	}

	if !isValidRef(ref) {
		return Pin{}, false
	}

	return Pin{
		Owner: owner,
		Repo:  repo,
		Ref:   ref,
	}.Canonical(), true
}

// looksLikeAlgoHex checks if a string matches the "algo-hexdigest" pattern
// (e.g. "sha1-abc123..." or "sha256-def456...").
func looksLikeAlgoHex(s string) bool {
	dashIdx := strings.IndexByte(s, '-')
	if dashIdx <= 0 || dashIdx == len(s)-1 {
		return false
	}
	algo := s[:dashIdx]
	if algo != "sha1" && algo != "sha256" {
		return false
	}
	hex := s[dashIdx+1:]
	for _, c := range hex {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return len(hex) > 0
}

// canonicalizeActionsV001 canonicalizes dependency keys from v0.0.1 format.
// It strips the :algo-hex suffix and normalizes owner/repo casing.
func canonicalizeActionsV001(f *File) (string, error) {
	if len(f.Dependencies) == 0 {
		return "", nil
	}
	out := make(map[string]Action, len(f.Dependencies))
	for key, action := range f.Dependencies {
		pin, ok := parsePinV001(key)
		if !ok {
			return key, fmt.Errorf("dependency key %q is not a valid v0.0.1 pin", key)
		}
		canonical := pin.String()
		if len(action.Uses) > 0 {
			canonUses := make([]string, len(action.Uses))
			for i, u := range action.Uses {
				uPin, uOk := parsePinV001(u)
				if !uOk {
					return key, fmt.Errorf("uses entry %q in dependency %q is not a valid v0.0.1 pin", u, key)
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

// canonicalizeWorkflowDepsV001 canonicalizes workflow dependency entries from
// v0.0.1 format (strips :algo-hex suffix).
func canonicalizeWorkflowDepsV001(f *File) (string, string, error) {
	for path, deps := range f.Workflows {
		if len(deps) == 0 {
			continue
		}
		canonicalized := make([]string, len(deps))
		for i, dep := range deps {
			pin, ok := parsePinV001(dep)
			if !ok {
				return path, dep, fmt.Errorf("workflow %q dependency %q is not a valid v0.0.1 pin", path, dep)
			}
			canonicalized[i] = pin.String()
		}
		f.Workflows[path] = canonicalized
	}
	return "", "", nil
}

// validateKnownFieldsVersioned is the version-aware variant of
// validateKnownFields, selecting the allowed/required key sets per version.
func validateKnownFieldsVersioned(f *File, paths []string, version string) *ParseError {
	allowed := allowedActionKeys
	required := requiredActionKeys

	if version == "v0.0.1" {
		allowed = allowedActionKeysV001
		required = requiredActionKeysV001
	}

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
			if _, ok := allowed[ak.Value]; !ok {
				return &ParseError{
					Line:   ak.Line,
					Column: ak.Column,
					Msg:    fmt.Sprintf("unknown action field %q for dependency %q", ak.Value, pinKey.Value),
				}
			}
			present[ak.Value] = struct{}{}
		}
		for _, req := range required {
			if _, ok := present[req]; !ok {
				return &ParseError{
					Line:   pinKey.Line,
					Column: pinKey.Column,
					Msg:    fmt.Sprintf("missing required action field %q for dependency %q", req, pinKey.Value),
				}
			}
		}
		if pe := rejectZeroValues(action, pinKey.Value); pe != nil {
			return pe
		}
		// These checks only apply for v0.0.2+ (owner/repo@ref pin keys).
		if version != "v0.0.1" {
			if pe := rejectKeyRefMismatch(pinKey, action); pe != nil {
				return pe
			}
			if pe := rejectFullSHACommitMismatch(pinKey, action); pe != nil {
				return pe
			}
		}
	}
	return nil
}

// rejectDuplicateDependencyKeys walks the top-level `dependencies` mapping and
// returns a positioned ParseError on the first duplicate key. yaml.v3's Decode
// would reject duplicates too, but with a generic message; this yields a
// domain-specific one. Defensive: any unexpected node shape returns nil and
// lets the normal decode path handle it.
func rejectDuplicateDependencyKeys(root *yaml.Node) *ParseError {
	m := docMapping(root)
	if m == nil {
		return nil
	}
	_, deps := mappingEntry(m, "dependencies")
	if deps == nil || deps.Kind != yaml.MappingNode {
		return nil
	}
	seen := make(map[string]int, len(deps.Content)/2)
	for i := 0; i+1 < len(deps.Content); i += 2 {
		k := deps.Content[i]
		if first, dup := seen[k.Value]; dup {
			return &ParseError{
				Line:   k.Line,
				Column: k.Column,
				Msg:    fmt.Sprintf("duplicate dependency key %q in lockfile dependencies (first defined at line %d)", k.Value, first),
			}
		}
		seen[k.Value] = k.Line
	}
	return nil
}

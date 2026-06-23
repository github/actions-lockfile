package lockfile

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_VersionRequired(t *testing.T) {
	_, err := Parse([]byte(`dependencies: {}` + "\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version is required")
}

func TestParse_UnsupportedVersion(t *testing.T) {
	// A version that isn't well-formed semver is rejected with the generic
	// "unsupported" message — no upgrade-path hint, since we can't tell if
	// the user is behind or just looking at garbage.
	_, err := Parse([]byte("version: garbage\ndependencies: {}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported dependency lockfile version")
	assert.False(t, errors.Is(err, ErrFutureVersion))
}

func TestParse_FutureVersion_ReturnsFriendlyError(t *testing.T) {
	_, err := Parse([]byte("version: v0.0.999\ndependencies: {}\n"))
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "v0.0.999", "should name the lockfile version")
	assert.Contains(t, msg, Version, "should name the supported version")
	assert.Contains(t, msg, "upgrade", "should tell the user to upgrade")
	// The library must stay tool-agnostic: ErrFutureVersion is consumed by
	// external readers (Dependabot, actions-workflow-parser), so the message
	// must not name a specific wrapping CLI. Consumers append their own
	// upgrade instructions off errors.Is(err, ErrFutureVersion).
	assert.NotContains(t, msg, "gh-actions-lock", "library message must not name a specific consumer tool")
}

func TestParse_FutureVersion_IsErrFutureVersion(t *testing.T) {
	_, err := Parse([]byte("version: v9.0.0\ndependencies: {}\n"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFutureVersion), "future-version error should match ErrFutureVersion sentinel")
}

func TestParse_WrongShapeReportsLine(t *testing.T) {
	// A workflow value shaped as a mapping instead of the expected sequence of
	// pin keys fails yaml type-decoding. Parse must surface the failing line as
	// structured data (ParseError.Line) and strip yaml.v3's "yaml:" prefix from
	// the reason so consumers don't misattribute the position to their own file.
	yaml := `version: v0.0.2
dependencies: {}
workflows:
  .github/workflows/ci.yml:
    dependencies:
      - actions/checkout@v6
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)

	var pe *ParseError
	require.True(t, errors.As(err, &pe), "expected a *ParseError, got %T", err)
	assert.Greater(t, pe.Line, 0, "expected a lockfile line number")
	assert.NotContains(t, pe.Msg, "yaml:", "yaml package prefix must be stripped from the reason")
}

func TestParse_UnsupportedVersionReportsPosition(t *testing.T) {
	// A semantic failure Parse detects itself must carry both line and column,
	// resolved by walking the retained YAML node tree to the offending value.
	yaml := `version: v9
dependencies: {}
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)

	var pe *ParseError
	require.True(t, errors.As(err, &pe), "expected a *ParseError, got %T", err)
	assert.Equal(t, 1, pe.Line, "version value is on line 1")
	assert.Greater(t, pe.Column, 0, "expected a column for a positioned semantic error")
}

func TestParse_DuplicateActionKeyReportsPosition(t *testing.T) {
	// The conflicting key must be located in the source tree so the position
	// points at a real offending dependency entry.
	yaml := `version: v0.0.2
dependencies:
  actions/checkout@v6:
    owner_id: 1234
    repo_id: 5678
  Actions/Checkout@v6:
    owner_id: 9999
    repo_id: 1
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)

	var pe *ParseError
	require.True(t, errors.As(err, &pe), "expected a *ParseError, got %T", err)
	assert.Greater(t, pe.Line, 0, "expected a line for the conflicting key")
	assert.Greater(t, pe.Column, 0, "expected a column for the conflicting key")
}

func TestParse_PositionLookup(t *testing.T) {
	// The retained node tree is exposed for consumer diagnostics via
	// Position/KeyPosition.
	yaml := `version: v0.0.2
dependencies: {}
workflows:
  .github/workflows/ci.yml: []
`
	f, err := Parse([]byte(yaml))
	require.NoError(t, err)

	line, col, ok := f.Position("version")
	require.True(t, ok)
	assert.Equal(t, 1, line)
	assert.Greater(t, col, 0)

	kl, kc, ok := f.KeyPosition("workflows", ".github/workflows/ci.yml")
	require.True(t, ok)
	assert.Equal(t, 4, kl, "workflow key is on line 4")
	assert.Greater(t, kc, 0)

	_, _, ok = f.Position("nope")
	assert.False(t, ok, "missing path resolves to ok=false")
}

func TestParse_CanonicalizesActionKeys(t *testing.T) {
	const canonical = "actions/checkout@v6"
	yaml := `version: v0.0.2
dependencies:
  Actions/Checkout@v6:
    ref: v6
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
workflows:
  .github/workflows/ci.yml:
    - Actions/Checkout@v6
`
	f, err := Parse([]byte(yaml))
	require.NoError(t, err)

	// Action map key is canonicalized so a lookup by Pin.String() hits.
	meta, ok := f.Dependencies[canonical]
	require.True(t, ok, "expected canonical key %q in dependencies; got keys: %v", canonical, mapKeys(f.Dependencies))
	assert.Equal(t, int64(1234), meta.OwnerID)
	assert.Equal(t, int64(5678), meta.RepoID)

	// Workflow dependency entries are canonicalized too.
	wf, ok := f.Workflows[".github/workflows/ci.yml"]
	require.True(t, ok)
	require.Len(t, wf, 1)
	assert.Equal(t, canonical, wf[0])
}

func TestParse_ConflictingActionKeyCasings(t *testing.T) {
	// Two source-casings of the same pin with differing metadata is
	// ambiguous and must be rejected.
	yaml := `version: v0.0.2
dependencies:
  actions/checkout@v6:
    ref: v6
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
  Actions/Checkout@v6:
    ref: v6
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 9999
    repo_id: 1
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate action key")
}

func TestParse_DuplicateActionKeyCasingsSameMetadataOK(t *testing.T) {
	// Same metadata on two casings collapses to one canonical entry.
	yaml := `version: v0.0.2
dependencies:
  actions/checkout@v6:
    ref: v6
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
  Actions/Checkout@v6:
    ref: v6
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
`
	f, err := Parse([]byte(yaml))
	require.NoError(t, err)
	assert.Len(t, f.Dependencies, 1)
}

func TestParse_UnparseableActionKeyRejected(t *testing.T) {
	// v0.0.1 rejects dependency keys that do not parse as valid pins.
	input := `version: v0.0.2
dependencies:
  "not a pin":
    ref: v4
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1
    repo_id: 2
`
	_, err := Parse([]byte(input))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid pin")
}

func TestParse_WorkflowPathKeyNotCanonicalized(t *testing.T) {
	// File paths are case-sensitive on Linux; do not normalize them.
	yaml := `version: v0.0.2
dependencies: {}
workflows:
  .github/workflows/CI.yml: []
`
	f, err := Parse([]byte(yaml))
	require.NoError(t, err)
	_, ok := f.Workflows[".github/workflows/CI.yml"]
	assert.True(t, ok)
}

func TestParse_RefRoundTrip(t *testing.T) {
	yaml := `version: v0.0.2
dependencies:
  actions/checkout@v6:
    ref: v6
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
  actions/internal@trunk:
    ref: trunk
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 1
    repo_id: 2
workflows: {}
`
	f, err := Parse([]byte(yaml))
	require.NoError(t, err)

	withTag := f.Dependencies["actions/checkout@v6"]
	assert.Equal(t, "v6", withTag.Ref)

	branchRef := f.Dependencies["actions/internal@trunk"]
	assert.Equal(t, "trunk", branchRef.Ref)
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── Security hardening tests ──────────────────────────────────────────────────

func TestParse_CommitEmptyStringRejected(t *testing.T) {
	// commit:"" must be rejected: an empty commit SHA disables every downstream
	// integrity check that reads Action.Commit, silently converting a
	// required field into a no-op.
	yaml := `version: v0.0.2
dependencies:
  actions/checkout@v4:
    ref: v4
    commit: ""
    owner_id: 1
    repo_id: 1
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"commit"`)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestParse_CommitInvalidFormatRejected(t *testing.T) {
	// A non-empty commit that isn't a valid algo-hex digest must be rejected.
	// "notadigest", "sha1-", and "HEAD" look plausible but carry no integrity
	// guarantee; consumers checking the algo and hex individually would silently
	// accept them, defeating the lockfile's purpose.
	cases := []struct {
		name   string
		commit string
	}{
		{"arbitrary string", "notadigest"},
		{"no hex after dash", "sha1-"},
		{"wrong length hex", "sha1-abc123"},
		{"symbolic ref", "HEAD"},
		{"sha1 prefix only", "sha1"},
		{"non-hex chars", "sha1-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			y := "version: v0.0.2\ndependencies:\n" +
				"  actions/checkout@v4:\n" +
				"    ref: v4\n" +
				"    commit: " + tc.commit + "\n" +
				"    owner_id: 1\n" +
				"    repo_id: 1\n"
			_, err := Parse([]byte(y))
			require.Error(t, err, "commit %q should be rejected", tc.commit)
			assert.Contains(t, err.Error(), "commit")
		})
	}
}

func TestParse_CommitValidFormatsAccepted(t *testing.T) {
	cases := []string{
		"sha1-11bd71901bbe5b1630ceea73d27597364c9af683",
		"sha256-a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	}
	for _, commit := range cases {
		t.Run(commit[:10], func(t *testing.T) {
			y := "version: v0.0.2\ndependencies:\n  actions/checkout@v4:\n" +
				"    ref: v4\n    commit: " + commit + "\n    owner_id: 1\n    repo_id: 1\n"
			_, err := Parse([]byte(y))
			require.NoError(t, err)
		})
	}
}

func TestParse_WorkflowPathTraversalRejected(t *testing.T) {
	// Workflow map keys are consumed as file paths by callers — accepting
	// "../../../etc/passwd" or "/etc/shadow" as a key is an arbitrary read
	// primitive for any consumer that calls os.Open(key).
	cases := []struct {
		name string
		key  string
	}{
		{"parent traversal", "../../../etc/passwd"},
		{"embedded traversal", ".github/../../../etc/passwd"},
		{"absolute path", "/etc/shadow"},
		{"double-dot segment", ".github/workflows/../../evil.yml"},
		{"windows absolute", "C:/Windows/system.ini"},
		{"backslash traversal", "..\\\\..\\\\secret"},
		{"UNC path", "\\\\\\\\server\\\\share"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			y := "version: v0.0.2\ndependencies: {}\nworkflows:\n  " + tc.key + ": []\n"
			_, err := Parse([]byte(y))
			require.Error(t, err, "workflow key %q should be rejected", tc.key)
			assert.Contains(t, err.Error(), "workflow path key")
		})
	}
}

func TestParse_WorkflowPathLegitimateKeysAccepted(t *testing.T) {
	legit := []string{
		".github/workflows/ci.yml",
		".github/workflows/release.yaml",
		"custom/path/workflow.yml",
	}
	for _, key := range legit {
		t.Run(key, func(t *testing.T) {
			y := "version: v0.0.2\ndependencies: {}\nworkflows:\n  " + key + ": []\n"
			_, err := Parse([]byte(y))
			require.NoError(t, err)
		})
	}
}

func TestParse_OversizedInputRejected(t *testing.T) {
	// An input larger than MaxParseSize must be rejected before any YAML
	// parsing so that memory-exhaustion DoS from oversized documents is
	// prevented at the library boundary.
	oversized := make([]byte, MaxParseSize+1)
	for i := range oversized {
		oversized[i] = 'x'
	}
	_, err := Parse(oversized)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

func TestParse_ExactMaxSizeAccepted(t *testing.T) {
	// A document at exactly MaxParseSize must not be size-rejected
	// (it will fail for other reasons, but not the size check).
	atMax := make([]byte, MaxParseSize)
	for i := range atMax {
		atMax[i] = 'x'
	}
	_, err := Parse(atMax)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "too large")
}

func TestParse_UsesCycleRejected(t *testing.T) {
	yaml := `version: v0.0.2
dependencies:
  actions/a@v1:
    ref: v1
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
    uses:
      - actions/b@v1
  actions/b@v1:
    ref: v1
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 2
    repo_id: 2
    uses:
      - actions/a@v1
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestParse_UsesSelfCycleRejected(t *testing.T) {
	yaml := `version: v0.0.2
dependencies:
  actions/a@v1:
    ref: v1
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
    uses:
      - actions/a@v1
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestParse_UsesAcyclicAccepted(t *testing.T) {
	// A valid DAG (A uses B, B has no uses) must parse successfully.
	yaml := `version: v0.0.2
dependencies:
  actions/a@v1:
    ref: v1
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
    uses:
      - actions/b@v1
  actions/b@v1:
    ref: v1
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 2
    repo_id: 2
`
	_, err := Parse([]byte(yaml))
	require.NoError(t, err)
}

func TestParse_KeyBodyRefMismatchRejected(t *testing.T) {
	// The pin key says "@v4" but the body says ref: v3 — this must be
	// rejected because the mismatch means the lockfile was hand-edited
	// inconsistently and cannot be trusted.
	yaml := `version: v0.0.2
dependencies:
  actions/checkout@v4:
    ref: v3
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match pin key ref")
	assert.Contains(t, err.Error(), `"v3"`)
	assert.Contains(t, err.Error(), `"v4"`)
}

func TestParse_KeyBodyRefMatchAccepted(t *testing.T) {
	// Key and body ref agree — must parse successfully.
	yaml := `version: v0.0.2
dependencies:
  actions/checkout@v4:
    ref: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
`
	_, err := Parse([]byte(yaml))
	require.NoError(t, err)
}

func TestParse_FullSHARefCommitMismatchRejected(t *testing.T) {
	// Pin key ref is a full SHA but commit encodes a different digest.
	input := `version: v0.0.2
dependencies:
  actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683:
    ref: 11bd71901bbe5b1630ceea73d27597364c9af683
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 1
    repo_id: 1
`
	_, err := Parse([]byte(input))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit digest")
	assert.Contains(t, err.Error(), "does not match")
}

func TestParse_FullSHARefCommitMatchAccepted(t *testing.T) {
	// Pin key ref is a full SHA and commit agrees — must parse.
	input := `version: v0.0.2
dependencies:
  actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683:
    ref: 11bd71901bbe5b1630ceea73d27597364c9af683
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
`
	_, err := Parse([]byte(input))
	require.NoError(t, err)
}

func TestParse_SymbolicRefDifferentCommitAccepted(t *testing.T) {
	// Symbolic ref (not a full SHA) — commit can be anything valid.
	input := `version: v0.0.2
dependencies:
  actions/checkout@v4:
    ref: v4
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 1
    repo_id: 1
`
	_, err := Parse([]byte(input))
	require.NoError(t, err)
}

func TestParse_ColonInRefRejected(t *testing.T) {
	// Colons in refs cause a pin key mismatch (pin key ref is "v4" but body ref is "v4:foo").
	input := `version: v0.0.2
dependencies:
  actions/checkout@v4:
    ref: "v4:foo"
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
`
	_, err := Parse([]byte(input))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match pin key ref")
}

func TestParse_LegacyDigestSuffixedKeyRejected(t *testing.T) {
	// Legacy v0.0.1 format used "owner/repo@ref:sha1-<hex>" as keys.
	// v0.0.1 rejects these because the colon makes the ref invalid.
	input := "version: v0.0.2\ndependencies:\n" +
		"  \"actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683\":\n" +
		"    ref: v4\n" +
		"    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683\n" +
		"    owner_id: 1\n" +
		"    repo_id: 1\n"
	_, err := Parse([]byte(input))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid pin")
}

func TestParse_LegacyDigestSuffixedUsesEntryRejected(t *testing.T) {
	// Valid dependency key but its uses entry is a legacy digest-suffixed pin.
	input := "version: v0.0.2\ndependencies:\n" +
		"  actions/cache@v4:\n" +
		"    ref: v4\n" +
		"    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683\n" +
		"    owner_id: 1\n" +
		"    repo_id: 1\n" +
		"  actions/composite@v1:\n" +
		"    ref: v1\n" +
		"    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
		"    owner_id: 2\n" +
		"    repo_id: 3\n" +
		"    uses:\n" +
		"      - \"actions/cache@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683\"\n"
	_, err := Parse([]byte(input))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid pin")
}

func TestParse_LegacyDigestSuffixedWorkflowDepRejected(t *testing.T) {
	// Workflow dependency entries with legacy format are rejected.
	input := "version: v0.0.2\ndependencies:\n" +
		"  actions/checkout@v4:\n" +
		"    ref: v4\n" +
		"    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683\n" +
		"    owner_id: 1\n" +
		"    repo_id: 1\n" +
		"workflows:\n" +
		"  .github/workflows/ci.yml:\n" +
		"    - \"actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683\"\n"
	_, err := Parse([]byte(input))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid pin")
}

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
	yaml := `version: v0.0.1
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
	yaml := `version: v0.0.1
dependencies:
  actions/checkout@v6:sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8:
    owner_id: 1234
    repo_id: 5678
  Actions/Checkout@v6:SHA1-8E8C483DB84B4BEE98B60C0593521ED34D9990E8:
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
	yaml := `version: v0.0.1
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
	const canonical = "actions/checkout@v6:sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8"
	yaml := `version: v0.0.1
dependencies:
  Actions/Checkout@v6:SHA1-8E8C483DB84B4BEE98B60C0593521ED34D9990E8:
    ref: v4
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
workflows:
  .github/workflows/ci.yml:
    - Actions/Checkout@v6:SHA1-8E8C483DB84B4BEE98B60C0593521ED34D9990E8
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
	yaml := `version: v0.0.1
dependencies:
  actions/checkout@v6:sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8:
    ref: v4
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
  Actions/Checkout@v6:SHA1-8E8C483DB84B4BEE98B60C0593521ED34D9990E8:
    ref: v4
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
	yaml := `version: v0.0.1
dependencies:
  actions/checkout@v6:sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8:
    ref: v4
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
  Actions/Checkout@v6:SHA1-8E8C483DB84B4BEE98B60C0593521ED34D9990E8:
    ref: v4
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
`
	f, err := Parse([]byte(yaml))
	require.NoError(t, err)
	assert.Len(t, f.Dependencies, 1)
}

func TestParse_UnparseableActionKeyPreserved(t *testing.T) {
	// Garbage keys are preserved verbatim so structural diagnostics can
	// surface them; Parse itself is not the validator.
	yaml := `version: v0.0.1
dependencies:
  "not a pin":
    ref: v4
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1
    repo_id: 2
`
	f, err := Parse([]byte(yaml))
	require.NoError(t, err)
	_, ok := f.Dependencies["not a pin"]
	assert.True(t, ok)
}

func TestParse_WorkflowPathKeyNotCanonicalized(t *testing.T) {
	// File paths are case-sensitive on Linux; do not normalize them.
	yaml := `version: v0.0.1
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
	yaml := `version: v0.0.1
dependencies:
  actions/checkout@v6:sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8:
    ref: v6
    commit: sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8
    owner_id: 1234
    repo_id: 5678
  actions/internal@trunk:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    ref: trunk
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 1
    repo_id: 2
workflows: {}
`
	f, err := Parse([]byte(yaml))
	require.NoError(t, err)

	withTag := f.Dependencies["actions/checkout@v6:sha1-8e8c483db84b4bee98b60c0593521ed34d9990e8"]
	assert.Equal(t, "v6", withTag.Ref)

	branchRef := f.Dependencies["actions/internal@trunk:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]
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
	yaml := `version: v0.0.1
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
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
			y := "version: v0.0.1\ndependencies:\n" +
				"  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:\n" +
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
			pin := "actions/checkout@v4:" + commit
			y := "version: v0.0.1\ndependencies:\n  " + pin + ":\n" +
				"    ref: v4\n    commit: " + commit + "\n    owner_id: 1\n    repo_id: 1\n"
			_, err := Parse([]byte(y))
			require.NoError(t, err)
		})
	}
}

func TestParse_CommitMismatchWithPinKeyRejected(t *testing.T) {
	// The commit field in the action body must match the digest embedded in
	// the pin key. A mismatch is a trust-confusion attack: a consumer that
	// checks action.Commit trusts a different hash than the pin key they
	// used to look up the action.
	yaml := `version: v0.0.1
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    ref: v4
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 1
    repo_id: 1
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disagrees with pin key digest")
}

func TestParse_CommitMatchingPinKeyAccepted(t *testing.T) {
	// When commit matches the pin key digest, parse must succeed.
	yaml := `version: v0.0.1
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    ref: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
`
	_, err := Parse([]byte(yaml))
	require.NoError(t, err)
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
			y := "version: v0.0.1\ndependencies: {}\nworkflows:\n  " + tc.key + ": []\n"
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
			y := "version: v0.0.1\ndependencies: {}\nworkflows:\n  " + key + ": []\n"
			_, err := Parse([]byte(y))
			require.NoError(t, err)
		})
	}
}

func TestParse_RefInjectionCharsRejected(t *testing.T) {
	// ref values are used in GraphQL queries, log output, and sometimes
	// shell commands. Characters that survive YAML parsing but are unsafe
	// for downstream interpolation (backslash, `..`, whitespace) must be
	// rejected by our validator.
	cases := []struct {
		name    string
		yamlRef string // value as it appears in YAML (double-quoted to reach our validator)
	}{
		// YAML double-quoted escape \\ → literal backslash in parsed value
		{"backslash", `"main\\evil"`},
		// YAML double-quoted \t → literal tab
		{"tab", `"main\tevil"`},
		// YAML double-quoted \n → literal newline
		{"newline", `"main\nevil"`},
		// unquoted dotdot traversal
		{"dotdot", "../main"},
		// path traversal with semicolons
		{"semicolon traversal", `"../../etc/passwd; rm -rf /"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			y := "version: v0.0.1\ndependencies:\n" +
				"  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:\n" +
				"    ref: " + tc.yamlRef + "\n" +
				"    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683\n" +
				"    owner_id: 1\n    repo_id: 1\n"
			_, err := Parse([]byte(y))
			require.Error(t, err, "ref %q should be rejected", tc.yamlRef)
			assert.Contains(t, err.Error(), "unsafe characters")
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
	yaml := `version: v0.0.1
dependencies:
  actions/a@v1:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    ref: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
    uses:
      - actions/b@v1:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  actions/b@v1:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    ref: v4
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 2
    repo_id: 2
    uses:
      - actions/a@v1:sha1-11bd71901bbe5b1630ceea73d27597364c9af683
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestParse_UsesSelfCycleRejected(t *testing.T) {
	yaml := `version: v0.0.1
dependencies:
  actions/a@v1:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    ref: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
    uses:
      - actions/a@v1:sha1-11bd71901bbe5b1630ceea73d27597364c9af683
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestParse_UsesAcyclicAccepted(t *testing.T) {
	// A valid DAG (A uses B, B has no uses) must parse successfully.
	yaml := `version: v0.0.1
dependencies:
  actions/a@v1:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    ref: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 1
    uses:
      - actions/b@v1:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  actions/b@v1:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    ref: v4
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 2
    repo_id: 2
`
	_, err := Parse([]byte(yaml))
	require.NoError(t, err)
}

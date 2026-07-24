package lockfile

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── v0.0.1 compat tests ─────────────────────────────────────────────────────

func TestParse_V001_TagBranchMigratedToRef(t *testing.T) {
	// A v0.0.1 lockfile with tag/branch fields gets migrated to ref.
	input := `version: v0.0.1
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    tag: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 2
  actions/internal@trunk:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    branch: trunk
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 3
    repo_id: 4
`
	f, err := Parse([]byte(input))
	require.NoError(t, err)

	// Version is normalized to latest.
	assert.Equal(t, Version, f.Version)

	// Pin keys are canonicalized to the v0.0.2 format (no :algo-hex suffix).
	checkout := f.Dependencies["actions/checkout@v4"]
	assert.Equal(t, "v4", checkout.Ref)
	assert.Equal(t, "sha1-11bd71901bbe5b1630ceea73d27597364c9af683", checkout.Commit)

	internal := f.Dependencies["actions/internal@trunk"]
	assert.Equal(t, "trunk", internal.Ref)
}

func TestParse_V001_TagWinsOverBranch(t *testing.T) {
	input := `version: v0.0.1
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    tag: v4
    branch: main
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 2
`
	f, err := Parse([]byte(input))
	require.NoError(t, err)

	checkout := f.Dependencies["actions/checkout@v4"]
	assert.Equal(t, "v4", checkout.Ref, "tag should win over branch in BestRef")
}

func TestParse_V001_NoTagNoBranch_FallsBackToPinRef(t *testing.T) {
	// If neither tag nor branch is present, ref is extracted from the pin key.
	input := `version: v0.0.1
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 2
`
	f, err := Parse([]byte(input))
	require.NoError(t, err)

	checkout := f.Dependencies["actions/checkout@v4"]
	assert.Equal(t, "v4", checkout.Ref, "ref should be extracted from pin key when tag/branch absent")
}

func TestParse_V001_LegacyPinKeysCanonicalizedToSimpleFormat(t *testing.T) {
	input := `version: v0.0.1
workflows:
  .github/workflows/ci.yml:
    - actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    tag: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 2
`
	f, err := Parse([]byte(input))
	require.NoError(t, err)

	// Dependency key is canonicalized to simple format.
	_, ok := f.Dependencies["actions/checkout@v4"]
	assert.True(t, ok, "expected simple key format after canonicalization")

	// Workflow deps are also canonicalized.
	wf := f.Workflows[".github/workflows/ci.yml"]
	require.Len(t, wf, 1)
	assert.Equal(t, "actions/checkout@v4", wf[0])
}

func TestParse_V001_RefFieldIsRejected(t *testing.T) {
	// v0.0.1 does not allow the "ref" field — it uses tag/branch.
	input := `version: v0.0.1
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    ref: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 2
`
	_, err := Parse([]byte(input))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown action field "ref"`)
}

func TestParse_V001_UsesEntriesCanonicalized(t *testing.T) {
	input := `version: v0.0.1
dependencies:
  actions/composite@v1:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    tag: v1
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 1
    repo_id: 1
    uses:
      - actions/cache@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683
  actions/cache@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    tag: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 2
    repo_id: 3
`
	f, err := Parse([]byte(input))
	require.NoError(t, err)

	composite := f.Dependencies["actions/composite@v1"]
	require.Len(t, composite.Uses, 1)
	assert.Equal(t, "actions/cache@v4", composite.Uses[0])
}

// ── VersionPolicy tests ──────────────────────────────────────────────────────

func TestParseWithPolicy_AcceptsVersionInRange(t *testing.T) {
	input := `version: v0.0.2
dependencies:
  actions/checkout@v4:
    ref: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 2
`
	policy := VersionPolicy{Min: "v0.0.1", Max: "v0.0.2"}
	f, err := ParseWithPolicy([]byte(input), policy)
	require.NoError(t, err)
	assert.Equal(t, Version, f.Version)
}

func TestParseWithPolicy_RejectsVersionBelowMin(t *testing.T) {
	input := `version: v0.0.1
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    tag: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 2
`
	policy := VersionPolicy{Min: "v0.0.2", Max: "v0.0.2"}
	_, err := ParseWithPolicy([]byte(input), policy)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnsupportedVersion))
	assert.Contains(t, err.Error(), "older than this consumer supports")
}

func TestParseWithPolicy_RejectsVersionAboveMax(t *testing.T) {
	input := `version: v0.0.2
dependencies: {}
`
	policy := VersionPolicy{Min: "v0.0.1", Max: "v0.0.1"}
	_, err := ParseWithPolicy([]byte(input), policy)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFutureVersion))
	assert.Contains(t, err.Error(), "newer than this consumer supports")
}

func TestParseWithPolicy_V001Only(t *testing.T) {
	input := `version: v0.0.1
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    tag: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 1
    repo_id: 2
`
	policy := VersionPolicy{Min: "v0.0.1", Max: "v0.0.1"}
	f, err := ParseWithPolicy([]byte(input), policy)
	require.NoError(t, err)
	assert.Equal(t, Version, f.Version, "parsed file should be normalized to latest version")
	assert.Equal(t, "v4", f.Dependencies["actions/checkout@v4"].Ref)
}

func TestParseWithPolicy_UnknownFutureVersion(t *testing.T) {
	input := `version: v1.0.0
dependencies: {}
`
	policy := VersionPolicy{Min: "v0.0.1", Max: "v0.0.2"}
	_, err := ParseWithPolicy([]byte(input), policy)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFutureVersion))
}

func TestParsePinV001(t *testing.T) {
	cases := []struct {
		input   string
		wantOK  bool
		wantRef string
	}{
		{"actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683", true, "v4"},
		{"actions/checkout@v4", true, "v4"},
		{"ACTIONS/Checkout@v4:sha1-abc123", true, "v4"}, // algo-hex-ish stripped, case normalized
		{"org/repo@release/v1.2:sha256-" + "ab" + "cd" + "ef" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true, "release/v1.2"},
		{"not-a-pin", false, ""},
		{"actions/checkout", false, ""},
		{"actions/sub/path@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683", false, ""}, // sub-path rejected
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			pin, ok := parsePinV001(tc.input)
			assert.Equal(t, tc.wantOK, ok)
			if ok {
				assert.Equal(t, tc.wantRef, pin.Ref)
			}
		})
	}
}

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	assert.Equal(t, "v0.0.1", p.Min)
	assert.Equal(t, Version, p.Max)
}

func TestParse_DuplicateDependencyKey_ConflictingSHAs(t *testing.T) {
	// Two identical dependency keys with conflicting commit SHAs must be
	// rejected with a domain-specific, positioned parse error rather than
	// yaml.v3's generic "mapping key already defined" message.
	input := `version: 'v0.0.2'
workflows:
    '.github/workflows/scenario-duplicate-deps-conflicting-failure.yml':
        - 'nodeselector/actions-test-fixtures@dup-test'
dependencies:
    'nodeselector/actions-test-fixtures@dup-test':
        ref: 'dup-test'
        commit: 'sha1-33a384c001ed694ba938667a1d5ace65d6c49de3'
        owner_id: 29457092
        repo_id: 1203329948
    'nodeselector/actions-test-fixtures@dup-test':
        ref: 'dup-test'
        commit: 'sha1-0000000000000000000000000000000000000000'
        owner_id: 29457092
        repo_id: 1203329948
`
	for _, tc := range []struct {
		name  string
		parse func() (File, error)
	}{
		{"Parse", func() (File, error) { return Parse([]byte(input)) }},
		{"ParseWithPolicy", func() (File, error) { return ParseWithPolicy([]byte(input), DefaultPolicy()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.parse()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "duplicate dependency key")
			assert.Contains(t, err.Error(), "nodeselector/actions-test-fixtures@dup-test")

			// Must be the domain path (positioned *ParseError), not a bare
			// yaml.v3 error — this is what surfaces as a dispatch-time 422.
			var pe *ParseError
			require.ErrorAs(t, err, &pe)
			assert.NotZero(t, pe.Line, "duplicate key error must carry a line position")
		})
	}
}

package lockfile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseActionRef(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNil  bool
		wantNWO  string
		wantPath string
		wantRef  string
	}{
		{name: "simple action", input: "actions/checkout@v4", wantNWO: "actions/checkout", wantRef: "v4"},
		{name: "path action", input: "actions/cache/save@v4", wantNWO: "actions/cache", wantPath: "save", wantRef: "v4"},
		{name: "SHA ref", input: "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683", wantNWO: "actions/checkout", wantRef: "11bd71901bbe5b1630ceea73d27597364c9af683"},
		{name: "local path action", input: "./local-action", wantNil: true},
		{name: "docker action", input: "docker://alpine:3.18", wantNil: true},
		{name: "expression-based ref", input: "${{ matrix.action }}", wantNil: true},
		{name: "no ref", input: "actions/checkout", wantNil: true},
		{name: "empty ref", input: "actions/checkout@", wantNil: true},
		{name: "single segment", input: "checkout@v4", wantNil: true},
		{name: "reusable workflow yml", input: "owner/repo/.github/workflows/called.yml@v1", wantNil: true},
		{name: "reusable workflow yaml", input: "owner/repo/.github/workflows/called.yaml@main", wantNil: true},
		{name: "path action that is not a reusable workflow", input: "owner/repo/some/path@v1", wantNWO: "owner/repo", wantPath: "some/path", wantRef: "v1"},
		{name: "whitespace trimmed", input: "  actions/checkout@v4  ", wantNWO: "actions/checkout", wantRef: "v4"},
		{name: "empty owner segment", input: "/repo@v1", wantNil: true},
		{name: "empty name segment", input: "owner/@v1", wantNil: true},
		{name: "leading slash both empty", input: "/@v1", wantNil: true},
		{name: "owner with newline injection", input: "actions\n/checkout@v1", wantNil: true},
		{name: "owner with quote", input: `actions"/checkout@v1`, wantNil: true},
		{name: "owner with space", input: "actions /checkout@v1", wantNil: true},
		{name: "control char tab embedded", input: "actions/check\tout@v1", wantNil: true},
		{name: "nested folder containing reusable workflow path is not reusable", input: "owner/repo/tools/.github/workflows/x.yml@v1", wantNWO: "owner/repo", wantPath: "tools/.github/workflows/x.yml", wantRef: "v1"},
		{name: "path with space", input: "owner/repo/bad path@v1", wantNil: true},
		{name: "path with quotes", input: `owner/repo/bad"path@v1`, wantNil: true},
		{name: "path dotdot traversal", input: "owner/repo/../etc@v1", wantNil: true},
		{name: "path single dot segment", input: "owner/repo/./foo@v1", wantNil: true},
		{name: "path empty segment double slash", input: "owner/repo/a//b@v1", wantNil: true},
		{name: "ref containing @", input: "foo/bar@a@b", wantNWO: "foo/bar", wantRef: "a@b"},
		{name: "dotdot owner segment", input: "../repo@v1", wantNil: true},
		{name: "dotdot repo segment", input: "foo/..@v1", wantNil: true},
		{name: "dot repo segment", input: "foo/.@v1", wantNil: true},
		{name: "dot owner segment", input: "./repo@v1", wantNil: true},
		// Refs are not validated beyond structural checks (colons for pin
		// grammar). The workflow parser accepts any characters in refs.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseActionRef(tt.input)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantNWO, got.NWO())
			assert.Equal(t, tt.wantPath, got.Path)
			assert.Equal(t, tt.wantRef, got.Ref)
		})
	}
}

func TestActionRefNWO(t *testing.T) {
	assert.Equal(t, "actions/checkout", ActionRef{Owner: "actions", Repo: "checkout"}.NWO())
	assert.Equal(t, "", ActionRef{}.NWO())
}

func TestActionRefFullName(t *testing.T) {
	assert.Equal(t, "actions/checkout", ActionRef{Owner: "actions", Repo: "checkout"}.FullName())
	assert.Equal(t, "actions/cache/save", ActionRef{Owner: "actions", Repo: "cache", Path: "save"}.FullName())
}

func TestParseReusableWorkflowRef(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNil  bool
		wantNWO  string
		wantPath string
		wantRef  string
	}{
		{name: "simple reusable yml", input: "octo/repo/.github/workflows/ci.yml@v1", wantNWO: "octo/repo", wantPath: ".github/workflows/ci.yml", wantRef: "v1"},
		{name: "reusable yaml extension", input: "octo/repo/.github/workflows/ci.yaml@main", wantNWO: "octo/repo", wantPath: ".github/workflows/ci.yaml", wantRef: "main"},
		{name: "reusable pinned to sha", input: "octo/repo/.github/workflows/ci.yml@11bd71901bbe5b1630ceea73d27597364c9af683", wantNWO: "octo/repo", wantPath: ".github/workflows/ci.yml", wantRef: "11bd71901bbe5b1630ceea73d27597364c9af683"},
		// The bug this helper exists to prevent: a ref that itself contains
		// `@`. A naive last-`@` split would mis-derive the path; the first-`@`
		// split keeps the whole ref intact.
		{name: "ref containing at sign", input: "octo/repo/.github/workflows/ci.yml@release@2024", wantNWO: "octo/repo", wantPath: ".github/workflows/ci.yml", wantRef: "release@2024"},
		{name: "ref containing slash", input: "octo/repo/.github/workflows/ci.yml@feature/foo", wantNWO: "octo/repo", wantPath: ".github/workflows/ci.yml", wantRef: "feature/foo"},
		// Non-reusable shapes return nil.
		{name: "repository action is not reusable", input: "actions/checkout@v4", wantNil: true},
		{name: "path action is not reusable", input: "actions/cache/save@v4", wantNil: true},
		{name: "non-yaml file in workflows dir", input: "octo/repo/.github/workflows/ci.txt@v1", wantNil: true},
		{name: "nested workflows path not at prefix", input: "octo/repo/tools/.github/workflows/ci.yml@v1", wantNil: true},
		{name: "nested under workflows dir", input: "octo/repo/.github/workflows/sub/ci.yml@v1", wantNil: true},
		{name: "no path", input: "octo/repo@v1", wantNil: true},
		// Security boundary still applies.
		{name: "local reusable workflow", input: "./.github/workflows/ci.yml@v1", wantNil: true},
		{name: "expression ref", input: "${{ matrix.wf }}@v1", wantNil: true},
		{name: "path traversal in workflow path", input: "octo/repo/.github/workflows/../../x.yml@v1", wantNil: true},
		{name: "empty ref", input: "octo/repo/.github/workflows/ci.yml@", wantNil: true},
		{name: "control char in path", input: "octo/repo/.github/workflows/ci\t.yml@v1", wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseReusableWorkflowRef(tt.input)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantNWO, got.NWO())
			assert.Equal(t, tt.wantPath, got.Path)
			assert.Equal(t, tt.wantRef, got.Ref)
		})
	}
}

func TestReusableWorkflowRefNames(t *testing.T) {
	r := ReusableWorkflowRef{Owner: "octo", Repo: "repo", Path: ".github/workflows/ci.yml", Ref: "v1"}
	assert.Equal(t, "octo/repo", r.NWO())
	assert.Equal(t, "octo/repo/.github/workflows/ci.yml", r.FullName())
	assert.Equal(t, "", ReusableWorkflowRef{}.NWO())
}

func TestIsLocalReusableWorkflow(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "local reusable yml", input: "./.github/workflows/ci.yml", want: true},
		{name: "local reusable yaml", input: "./.github/workflows/ci.yaml", want: true},
		{name: "local composite action directory", input: "./my-action", want: false},
		{name: "local composite no extension", input: "./my-action/", want: false},
		{name: "missing ./ prefix yml", input: ".github/workflows/ci.yml", want: false},
		{name: "missing ./ prefix bare file", input: "ci.yml", want: false},
		{name: "remote action is not local", input: "actions/checkout@v4", want: false},
		{name: "remote reusable workflow is not local", input: "octo/repo/.github/workflows/ci.yml@v1", want: false},
		{name: "empty string", input: "", want: false},
		{name: "yaml outside .github/workflows still true", input: "./scripts/run.yml", want: true},
		{name: "local non-yaml extension", input: "./something.txt", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsLocalReusableWorkflow(tt.input))
		})
	}
}


func TestIsReusableWorkflow(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "reusable workflow yml", path: ".github/workflows/called.yml", want: true},
		{name: "reusable workflow yaml", path: ".github/workflows/called.yaml", want: true},
		{name: "regular path action", path: "save", want: false},
		{name: "no path", path: "", want: false},
		{name: "nested under workflows dir", path: ".github/workflows/sub/called.yml", want: false},
		{name: "non-yaml under workflows dir", path: ".github/workflows/called.txt", want: false},
		{name: "workflows dir prefix only", path: ".github/workflows/", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isReusableWorkflow(tt.path))
		})
	}
}

// TestActionAndReusableParsersAreMutuallyExclusive locks in the security
// invariant: no single uses: string is accepted by both ParseActionRef and
// ParseReusableWorkflowRef.
func TestActionAndReusableParsersAreMutuallyExclusive(t *testing.T) {
	inputs := []string{
		"actions/checkout@v4",
		"actions/cache/save@v4",
		"octo/repo/.github/workflows/ci.yml@v1",
		"octo/repo/.github/workflows/ci.yaml@main",
		"octo/repo/.github/workflows/ci.yml@release@2024",
		"octo/repo/.github/workflows/sub/ci.yml@v1",
		"octo/repo/.github/workflows/ci.txt@v1",
		"./local@v1",
		"docker://alpine:3.18",
		"${{ matrix.x }}@v1",
		"owner/repo/tools/.github/workflows/x.yml@v1",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			a := ParseActionRef(in)
			r := ParseReusableWorkflowRef(in)
			if a != nil && r != nil {
				t.Fatalf("both parsers accepted %q (action=%+v reusable=%+v)", in, a, r)
			}
		})
	}
}

func TestIsFullSha(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "valid lowercase sha", input: "11bd71901bbe5b1630ceea73d27597364c9af683", want: true},
		{name: "valid uppercase sha", input: "11BD71901BBE5B1630CEEA73D27597364C9AF683", want: true},
		{name: "valid mixed case sha", input: "11bd71901BBE5b1630ceea73d27597364C9AF683", want: true},
		{name: "too short", input: "11bd71901bbe5b1630ceea73d2759736", want: false},
		{name: "too long", input: "11bd71901bbe5b1630ceea73d27597364c9af683aa", want: false},
		{name: "tag ref", input: "v4", want: false},
		{name: "branch ref", input: "main", want: false},
		{name: "empty", input: "", want: false},
		{name: "non-hex chars", input: "ggbd71901bbe5b1630ceea73d27597364c9af683", want: false},
		{name: "sha256 length", input: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", want: true},
		{name: "41 chars not valid", input: "11bd71901bbe5b1630ceea73d27597364c9af683a", want: false},
		{name: "63 chars not valid", input: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsFullSha(tt.input))
		})
	}
}

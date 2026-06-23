package lockfile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePin(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		want  Pin
	}{
		{
			name:  "owner repo",
			entry: "actions/checkout@v4",
			want: Pin{
				NWO:   "actions/checkout",
				Owner: "actions",
				Repo:  "checkout",
				Ref:   "v4",
			},
		},
		{
			name:  "full SHA ref",
			entry: "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
			want: Pin{
				NWO:   "actions/checkout",
				Owner: "actions",
				Repo:  "checkout",
				Ref:   "11bd71901bbe5b1630ceea73d27597364c9af683",
			},
		},
		{
			// Monorepo sub-action tags (e.g. attest-build-provenance's
			// predicate/) embed an '@' in the ref, producing a double-'@'
			// key. The first '@' bounds the NWO and the rest is the ref.
			name:  "ref containing at (monorepo sub-action tag)",
			entry: "actions/attest-build-provenance@predicate@1.1.4",
			want: Pin{
				NWO:   "actions/attest-build-provenance",
				Owner: "actions",
				Repo:  "attest-build-provenance",
				Ref:   "predicate@1.1.4",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParsePin(tt.entry)
			require.True(t, ok)
			assert.Equal(t, tt.want, got)
			// Round-trip: serializing the parsed pin reproduces the entry.
			assert.Equal(t, tt.entry, got.String())
		})
	}
}

func TestParsePin_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{"empty", ""},
		{"missing at", "actions/checkout"},
		{"empty owner", "/@v4"},
		{"empty repo", "actions/@v4"},
		{"sub-action path rejected", "actions/cache/save@v4"},
		{"deep sub-action path rejected", "owner/repo/a/b@v1"},
		{"colon in ref rejected", "actions/checkout@v4:foo"},
		{"colon only ref rejected", "actions/checkout@:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParsePin(tt.entry)
			assert.False(t, ok)
			assert.Equal(t, Pin{}, got)
		})
	}
}

func TestIndexKey(t *testing.T) {
	assert.Equal(t, "actions/checkout@v4", IndexKey("actions", "checkout", "v4"))
}

func TestPin_IndexKey(t *testing.T) {
	tests := []struct {
		name string
		pin  Pin
		want string
	}{
		{
			name: "owner repo",
			pin:  Pin{Owner: "actions", Repo: "checkout", Ref: "v4"},
			want: "actions/checkout@v4",
		},
		{
			name: "mixed case is lowercased",
			pin:  Pin{Owner: "Actions", Repo: "Checkout", Ref: "v4"},
			want: "actions/checkout@v4",
		},
		{
			name: "empty ref",
			pin:  Pin{Owner: "actions", Repo: "checkout"},
			want: "actions/checkout@",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.pin.IndexKey())
		})
	}
}

func TestParsePin_RefInjectionRejected(t *testing.T) {
	// ParsePin must reject refs containing characters that are unsafe for
	// URL paths and GraphQL string literals (spaces, quotes, backticks,
	// backslash). Without this check a caller receives a Pin.Ref that
	// injects into downstream shell commands or API calls.
	cases := []struct {
		name  string
		input string
	}{
		{"space in ref", "actions/checkout@v4 evil"},
		{"tab in ref", "actions/checkout@v4\tevil"},
		{"double-quote in ref", "actions/checkout@v4\"evil"},
		{"single-quote in ref", "actions/checkout@v4'evil"},
		{"backtick in ref", "actions/checkout@v4`evil"},
		{"backslash in ref", "actions/checkout@v4\\evil"},
		{"dotdot in ref", "actions/checkout@../evil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := ParsePin(tc.input)
			assert.False(t, ok, "ParsePin should reject ref with injection chars: %q", tc.input)
		})
	}
}

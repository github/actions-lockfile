package lockfile

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitRef(t *testing.T) {
	cases := []struct {
		ref    string
		tag    string
		branch string
	}{
		{"", "", ""},
		{"v4.3.1", "v4.3.1", ""},
		{"v1.0.0", "v1.0.0", ""},
		{"1.2.3", "1.2.3", ""},
		{"v4", "v4", ""},                       // major-only is still stable semver
		{"v4.2", "v4.2", ""},                   // major.minor is still stable semver
		{"v1.0.0-beta.1", "", "v1.0.0-beta.1"}, // pre-release → branch
		{"main", "", "main"},
		{"trunk", "", "trunk"},
		{"release/v4", "", "release/v4"},
		{"my-feature", "", "my-feature"},
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			tag, branch := SplitRef(tc.ref)
			assert.Equal(t, tc.tag, tag, "tag")
			assert.Equal(t, tc.branch, branch, "branch")
		})
	}
}

func TestBestRef(t *testing.T) {
	cases := []struct {
		tag    string
		branch string
		want   string
	}{
		{"v4.3.1", "main", "v4.3.1"},
		{"v4", "main", "v4"},
		{"", "main", "main"},
		{"", "", ""},
		{"v1.0.0", "", "v1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.tag+"/"+tc.branch, func(t *testing.T) {
			assert.Equal(t, tc.want, BestRef(tc.tag, tc.branch))
		})
	}
}

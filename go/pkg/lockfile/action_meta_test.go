package lockfile

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseActionMeta(t *testing.T) {
	tests := []struct {
		name       string
		file       string
		wantExec   ExecutionType
		wantNested int
	}{
		{name: "composite action", file: "testdata/composite_action.yml", wantExec: ExecComposite, wantNested: 2},
		{name: "node action", file: "testdata/node_action.yml", wantExec: ExecNode, wantNested: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := os.ReadFile(tt.file)
			if errors.Is(err, os.ErrNotExist) {
				t.Skip("testdata is not present in this module checkout (symlink to repo-root testdata is dropped from module zips)")
			}
			require.NoError(t, err)

			meta, err := ParseActionMeta(string(content))
			require.NoError(t, err)
			assert.Equal(t, tt.wantExec, meta.Execution)
			assert.Len(t, meta.NestedUses, tt.wantNested)
		})
	}
}

func TestParseActionMeta_YAMLAnchorsRejected(t *testing.T) {
	// Anchor definition
	withAnchor := `
name: anchored
runs:
  using: composite
  steps:
    - &step
      uses: actions/checkout@v4
    - *step
`
	_, err := ParseActionMeta(withAnchor)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anchors and aliases are not supported")
}

func TestParseActionMeta_OversizedInputRejected(t *testing.T) {
	// Build an input just over MaxActionMetaSize.
	oversized := "name: big\n" + strings.Repeat("# padding\n", (MaxActionMetaSize/10)+1)
	_, err := ParseActionMeta(oversized)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

func TestParseActionMeta_ExactMaxSizeAccepted(t *testing.T) {
	// A valid minimal composite action padded to exactly MaxActionMetaSize must be accepted.
	base := "name: padded\nruns:\n  using: composite\n  steps:\n    - uses: actions/checkout@v4\n"
	pad := strings.Repeat("#", MaxActionMetaSize-len(base))
	_, err := ParseActionMeta(base + pad)
	require.NoError(t, err)
}

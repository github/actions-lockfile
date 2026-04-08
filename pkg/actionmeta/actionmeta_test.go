package actionmeta

import (
	"testing"
)

func TestParseNodeAction(t *testing.T) {
	content := `
name: 'Simple Node'
runs:
  using: 'node20'
  main: 'index.js'
`
	meta, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Execution != ExecNode {
		t.Errorf("expected node, got %s", meta.Execution)
	}
	if len(meta.NestedUses) != 0 {
		t.Errorf("expected 0 nested uses, got %d", len(meta.NestedUses))
	}
}

func TestParseCompositeAction(t *testing.T) {
	content := `
name: 'Composite'
runs:
  using: 'composite'
  steps:
    - uses: actions/checkout@v4
    - run: echo hi
      shell: bash
    - uses: owner/other@v1
`
	meta, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Execution != ExecComposite {
		t.Errorf("expected composite, got %s", meta.Execution)
	}
	if len(meta.NestedUses) != 2 {
		t.Fatalf("expected 2 nested uses, got %d", len(meta.NestedUses))
	}
	if meta.NestedUses[0] != "actions/checkout@v4" {
		t.Errorf("nested[0] = %q", meta.NestedUses[0])
	}
	if meta.NestedUses[1] != "owner/other@v1" {
		t.Errorf("nested[1] = %q", meta.NestedUses[1])
	}
}

func TestParseDockerAction(t *testing.T) {
	content := `
name: 'Docker Action'
runs:
  using: 'docker'
  image: 'Dockerfile'
`
	meta, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Execution != ExecDocker {
		t.Errorf("expected docker, got %s", meta.Execution)
	}
}

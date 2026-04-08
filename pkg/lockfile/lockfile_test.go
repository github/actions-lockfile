package lockfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseActionRef(t *testing.T) {
	tests := []struct {
		input    string
		wantNil  bool
		wantNWO  string
		wantPath string
		wantRef  string
	}{
		{"actions/checkout@v4", false, "actions/checkout", "", "v4"},
		{"actions/cache/save@v4", false, "actions/cache", "save", "v4"},
		{"actions/cache/restore@v4", false, "actions/cache", "restore", "v4"},
		{"org/repo@11bd71901bbe5b1630ceea73d27597364c9af683", false, "org/repo", "", "11bd71901bbe5b1630ceea73d27597364c9af683"},
		{"./local-action", true, "", "", ""},
		{"docker://alpine:3.19", true, "", "", ""},
		{"invalid", true, "", "", ""},
		{"", true, "", "", ""},
		{"${{ matrix.action }}@v4", true, "", "", ""},                             // expression-based
		{"org/repo/.github/workflows/build.yml@main", false, "org/repo", ".github/workflows/build.yml", "main"}, // reusable workflow (pinned like actions)
		{"github/go-linter/install-only@abc123", false, "github/go-linter", "install-only", "abc123"}, // path action
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseActionRef(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("ParseActionRef(%q) = %+v, want nil", tt.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ParseActionRef(%q) = nil, want non-nil", tt.input)
			}
			if got.NWO() != tt.wantNWO {
				t.Errorf("NWO = %q, want %q", got.NWO(), tt.wantNWO)
			}
			if got.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", got.Path, tt.wantPath)
			}
			if got.Ref != tt.wantRef {
				t.Errorf("Ref = %q, want %q", got.Ref, tt.wantRef)
			}
		})
	}
}

func TestParseDependencyString(t *testing.T) {
	tests := []struct {
		input    string
		wantNWO  string
		wantRef  string
		wantSHA  string
		wantAlgo string
		wantErr  bool
	}{
		{
			"github.com/actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683",
			"actions/checkout", "v4", "11bd71901bbe5b1630ceea73d27597364c9af683", "sha1", false,
		},
		{
			"github.com/actions/cache@v4:sha1-abcdef1234567890abcdef1234567890abcdef12",
			"actions/cache", "v4", "abcdef1234567890abcdef1234567890abcdef12", "sha1", false,
		},
		{
			"github.com/actions/checkout@v5:sha256-abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
			"actions/checkout", "v5", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab", "sha256", false,
		},
		{"garbage", "", "", "", "", true},
		{"github.com/no-sha-here@v1", "", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseDependencyString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.NWO != tt.wantNWO {
				t.Errorf("NWO = %q, want %q", got.NWO, tt.wantNWO)
			}
			if got.Ref != tt.wantRef {
				t.Errorf("Ref = %q, want %q", got.Ref, tt.wantRef)
			}
			if got.SHA != tt.wantSHA {
				t.Errorf("SHA = %q, want %q", got.SHA, tt.wantSHA)
			}
			if got.HashAlgo != tt.wantAlgo {
				t.Errorf("HashAlgo = %q, want %q", got.HashAlgo, tt.wantAlgo)
			}
		})
	}
}

func TestDependencyRoundtrip(t *testing.T) {
	// SHA-1 roundtrip
	d1 := Dependency{NWO: "actions/checkout", Ref: "v4", SHA: "abc123"}
	s1 := d1.String()
	got1, err := ParseDependencyString(s1)
	if err != nil {
		t.Fatalf("sha1 roundtrip parse error: %v", err)
	}
	if got1.NWO != d1.NWO || got1.Ref != d1.Ref || got1.SHA != d1.SHA || got1.HashAlgo != "sha1" {
		t.Errorf("sha1 roundtrip mismatch: %+v", got1)
	}

	// SHA-256 roundtrip
	d2 := Dependency{NWO: "actions/checkout", Ref: "v5", SHA: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab", HashAlgo: "sha256"}
	s2 := d2.String()
	got2, err := ParseDependencyString(s2)
	if err != nil {
		t.Fatalf("sha256 roundtrip parse error: %v", err)
	}
	if got2.NWO != d2.NWO || got2.Ref != d2.Ref || got2.SHA != d2.SHA || got2.HashAlgo != "sha256" {
		t.Errorf("sha256 roundtrip mismatch: %+v", got2)
	}
	if !strings.Contains(s2, ":sha256-") {
		t.Errorf("sha256 string should contain :sha256-, got %q", s2)
	}
}

func TestExtractActionRefs(t *testing.T) {
	wf, err := Load(filepath.Join("..", "..", "testdata", "workflows", "mixed.yml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	refs, _, _ := wf.ExtractActionRefs()

	wantNWOs := map[string]bool{
		"actions/checkout":   false,
		"actions/setup-node": false,
	}

	for _, ref := range refs {
		if _, ok := wantNWOs[ref.NWO()]; ok {
			wantNWOs[ref.NWO()] = true
		} else {
			t.Errorf("unexpected ref: %s", ref.NWO())
		}
	}

	for nwo, found := range wantNWOs {
		if !found {
			t.Errorf("missing expected ref: %s", nwo)
		}
	}
}

func TestReadWriteDependencies(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "workflows", "basic.yml")
	content, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading source: %v", err)
	}
	tmp := filepath.Join(dir, "test.yml")
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		t.Fatalf("writing temp: %v", err)
	}

	wf, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	deps, err := wf.ReadDependencies()
	if err != nil {
		t.Fatalf("ReadDependencies: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps, got %d", len(deps))
	}

	newDeps := []Dependency{
		{NWO: "actions/checkout", Ref: "v4", SHA: "abc123"},
		{NWO: "actions/setup-go", Ref: "v5", SHA: "def456"},
	}
	output, err := wf.WriteDependencies(newDeps)
	if err != nil {
		t.Fatalf("WriteDependencies: %v", err)
	}
	if err := os.WriteFile(tmp, output, 0644); err != nil {
		t.Fatalf("writing output: %v", err)
	}

	wf2, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load after write: %v", err)
	}
	got, err := wf2.ReadDependencies()
	if err != nil {
		t.Fatalf("ReadDependencies after write: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(got))
	}

	// Re-pin should be idempotent
	output2, err := wf2.WriteDependencies(newDeps)
	if err != nil {
		t.Fatalf("WriteDependencies second time: %v", err)
	}
	if err := os.WriteFile(tmp, output2, 0644); err != nil {
		t.Fatalf("writing output2: %v", err)
	}
	wf3, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load after second write: %v", err)
	}
	got2, err := wf3.ReadDependencies()
	if err != nil {
		t.Fatalf("ReadDependencies after second write: %v", err)
	}
	if len(got2) != 2 {
		t.Fatalf("expected 2 deps after re-pin, got %d", len(got2))
	}
}

func TestExtractPathActions(t *testing.T) {
	wf, err := Load(filepath.Join("..", "..", "testdata", "workflows", "path-action.yml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	refs, _, _ := wf.ExtractActionRefs()
	if len(refs) != 2 {
		t.Fatalf("expected 2 deduplicated refs, got %d: %+v", len(refs), refs)
	}
}

func TestReadTamperedDependencies(t *testing.T) {
	wf, err := Load(filepath.Join("..", "..", "testdata", "workflows", "tampered.yml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	deps, err := wf.ReadDependencies()
	if err != nil {
		t.Fatalf("ReadDependencies: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(deps))
	}
}

func TestRejectDuplicateDeps(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "dup.yml")
	content := `name: dup
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: cli/cli@v2.50.0

dependencies:
  - github.com/cli/cli@v2.50.0:sha1-aaaa
  - github.com/cli/cli@v2.50.0:sha1-bbbb
`
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	wf, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = wf.ReadDependencies()
	if err == nil {
		t.Fatal("expected error for duplicate dependency keys, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got: %v", err)
	}
}

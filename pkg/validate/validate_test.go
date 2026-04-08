package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/github/actions-lockfile/pkg/pin"
)

// Integration test -- requires GITHUB_TOKEN or gh auth.
// Tests the full pin-then-validate cycle and tamper detection.
func TestPinThenValidate(t *testing.T) {
	if os.Getenv("GITHUB_TOKEN") == "" {
		// Try gh auth
		if _, err := os.Stat("/usr/local/bin/gh"); err != nil {
			t.Skip("skipping integration test: no GITHUB_TOKEN and gh not found")
		}
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("skipping: GITHUB_TOKEN required for integration tests")
	}

	// Copy basic workflow to temp dir
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

	// Pin
	if err := pin.Run(tmp, token, pin.Options{}); err != nil {
		t.Fatalf("pin failed: %v", err)
	}

	// Validate -- should pass
	result, err := Run(tmp, token)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid, got invalid: %v", result.Errors)
	}
}

func TestTamperDetection(t *testing.T) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("skipping: GITHUB_TOKEN required for integration tests")
	}

	src := filepath.Join("..", "..", "testdata", "workflows", "tampered.yml")

	result, err := Run(src, token)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if result.Valid {
		t.Error("expected validation to fail for tampered workflow, but it passed")
	}

	// Should have TAMPERED errors
	foundTamper := false
	for _, e := range result.Errors {
		if len(e) > 8 && e[:8] == "TAMPERED" {
			foundTamper = true
		}
	}
	if !foundTamper {
		t.Errorf("expected TAMPERED error, got: %v", result.Errors)
	}
}

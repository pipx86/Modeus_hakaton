package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestOfflineE2E runs the deterministic end-to-end command from the repository
// root and checks its summary. It is an external test on purpose: it exercises
// the product as a user would, without reaching into internal packages.
func TestOfflineE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("offline e2e is skipped in -short mode")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp(root, ".e2e-*.json")
	if err != nil {
		t.Fatal(err)
	}
	out := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(out)
	cmd := exec.Command("go", "run", "./cmd/e2e", "-out", out)
	cmd.Dir = root
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("offline e2e failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("e2e output was not created: %v", err)
	}
}

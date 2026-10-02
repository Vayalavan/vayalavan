package config

import (
	"os"
	"testing"
)

// TestLoadRootDotEnvOutsideARepoCheckout is the container case.
//
// Every Go service calls this at startup. When it returned an error for "no
// repository root", the services refused to boot inside their own Docker
// images — configuration comes from real environment variables there, and
// there is no .env to find. The failure looked like a config problem and was
// not.
func TestLoadRootDotEnvOutsideARepoCheckout(t *testing.T) {
	// A directory with no go.work and no .env.example above it, the way a
	// container's WORKDIR looks.
	dir := t.TempDir()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := LoadRootDotEnv(); err != nil {
		t.Errorf("LoadRootDotEnv() = %v outside a checkout, want nil — "+
			"this is what stopped the services booting in Docker", err)
	}
}

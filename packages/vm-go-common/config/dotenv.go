package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rootMarkers identify the repository root when walking up from the current
// directory. go.work is the reliable one — it exists only at the root and is
// required for the Go workspace to function at all.
var rootMarkers = []string{"go.work", ".env.example"}

// FindRepoRoot walks up from the current directory looking for the repository
// root, so a service started from its own directory still finds the single
// root .env.
//
// Returns an error rather than guessing: silently falling back to the cwd
// would load no .env and produce a confusing "DATABASE_URL is required" on a
// machine where the file plainly exists.
func FindRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("config: determining working directory: %w", err)
	}

	for {
		for _, marker := range rootMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding a marker.
			return "", fmt.Errorf(
				"config: could not locate the repository root (no go.work or .env.example in any parent directory)")
		}
		dir = parent
	}
}

// LoadRootDotEnv finds the repository root and loads its .env.
//
// One file for the whole platform (see .env.example). Values already present
// in the environment always win, so a real deployment's env vars are never
// overridden by a stray file, and `PROFILE_PORT=9999 go run ./cmd/api` works
// as an ad-hoc override.
//
// A missing .env is not an error: production sets real environment variables
// and ships no file.
func LoadRootDotEnv() error {
	root, err := FindRepoRoot()
	if err != nil {
		// No repo root means this is not a developer checkout — a container,
		// typically, where configuration arrives as real environment
		// variables and there is no .env to find.
		//
		// This used to return the error, which meant every Go service refused
		// to boot inside its own Docker image with the message "could not
		// locate the repository root", however correctly its environment was
		// configured. The comment above already promised a missing .env was
		// not an error; only the code disagreed.
		return nil
	}
	return LoadDotEnv(filepath.Join(root, ".env"))
}

// LoadDotEnv reads KEY=VALUE files into the process environment.
//
// Missing files are skipped silently; malformed lines are an error, because a
// typo in the one file the whole platform reads should be loud.
func LoadDotEnv(paths ...string) error {
	for _, path := range paths {
		if err := loadDotEnvFile(path); err != nil {
			return err
		}
	}
	return nil
}

func loadDotEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("config: opening %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		// Tolerate the "export KEY=value" form so the file can also be
		// sourced by a shell.
		text = strings.TrimPrefix(text, "export ")

		key, value, found := strings.Cut(text, "=")
		if !found {
			return fmt.Errorf("config: %s:%d: expected KEY=VALUE, got %q", path, line, text)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("config: %s:%d: empty key", path, line)
		}

		// Environment wins over file.
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, unquote(strings.TrimSpace(value))); err != nil {
			return fmt.Errorf("config: setting %s: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("config: reading %s: %w", path, err)
	}
	return nil
}

// unquote strips one matching pair of surrounding quotes. Unquoted values
// additionally have trailing ` #` comments removed; quoted values do not, so
// a password containing '#' survives intact.
func unquote(v string) string {
	if len(v) >= 2 {
		first, last := v[0], v[len(v)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return v[1 : len(v)-1]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

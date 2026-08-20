package cmd

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/social-sync/digestive/internal/templates"
	"github.com/spf13/cobra"
)

const (
	initEnvFile    = ".env"
	initConfigFile = "config.yaml"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create starter .env and config.yaml in the current directory",
	Long: "init writes a starter config.yaml and a .env containing a freshly " +
		"generated hashing key. It never overwrites: if either file already " +
		"exists, init fails and writes nothing.",
	Args: cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		created, err := runInit()
		return finish("init", initResult{Created: created}, nil, err, func() {
			fmt.Printf("created %s and %s\n", initEnvFile, initConfigFile)
			fmt.Printf("a random hashing key was written to %s (EXPORT_HASH_KEY); keep it stable across runs\n", initEnvFile)
			fmt.Printf("edit %s to set SINGLESTORE_DSN, then edit %s and run: digestive validate\n", initEnvFile, initConfigFile)
		})
	},
}

// initResult is the --json payload for a successful init. It reports which
// files were created; it never includes the generated hashing key, which lives
// only in .env.
type initResult struct {
	Created []string `json:"created"`
}

func init() {
	rootCmd.AddCommand(initCmd)
}

// runInit writes the starter files and returns the ones it created, in the
// order they were written. It never overwrites: if either target exists it
// writes nothing and returns an error.
func runInit() ([]string, error) {
	// Check both targets first so we never overwrite, and never leave a
	// partial result when one of the two already exists.
	var existing []string
	for _, f := range []string{initEnvFile, initConfigFile} {
		switch _, err := os.Stat(f); {
		case err == nil:
			existing = append(existing, f)
		case errors.Is(err, os.ErrNotExist):
			// good, does not exist
		default:
			return nil, fmt.Errorf("check %s: %w", f, err)
		}
	}
	if len(existing) > 0 {
		return nil, fmt.Errorf("refusing to overwrite existing file(s): %s", strings.Join(existing, ", "))
	}

	key, err := generateKey()
	if err != nil {
		return nil, err
	}

	// .env holds the secret, so restrict its permissions.
	env := fmt.Sprintf(templates.EnvFormat, key)
	if err := os.WriteFile(initEnvFile, []byte(env), 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", initEnvFile, err)
	}
	if err := os.WriteFile(initConfigFile, []byte(templates.Config), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", initConfigFile, err)
	}

	return []string{initEnvFile, initConfigFile}, nil
}

// generateKey returns a 256-bit cryptographically random key, base64url-encoded
// so it is safe to drop straight into a .env value.
func generateKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate hashing key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

package cmd

import (
	"context"
	"fmt"
	"net/mail"
	"path/filepath"
	"strings"
	"time"

	"github.com/social-sync/digestive/internal/audit"
	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/manifest"
	"github.com/spf13/cobra"
)

// Compliance flags shared by export and sync. They only bear meaning when a
// `compliance:` block is present in the config.
var (
	requesterName      string
	requesterEmail     string
	cleanupOnAuditFail bool
)

// addComplianceFlags registers the requester and audit-cleanup flags on a
// command. Both export and sync call it.
func addComplianceFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&requesterName, "requester-name", "", "name of the person requesting the export (required when compliance is configured)")
	cmd.Flags().StringVar(&requesterEmail, "requester-email", "", "email of the person requesting the export (required when compliance is configured)")
	cmd.Flags().BoolVar(&cleanupOnAuditFail, "cleanup-on-audit-fail", false, "delete the exported run directory if the audit record cannot be written")
}

// requireRequester validates the requester flags when compliance is on. It
// returns the validated Requester, or an error naming what is missing/invalid.
// It is a no-op (empty Requester, nil error) when compliance is off.
func requireRequester(cfg *config.Config) (audit.Requester, error) {
	if cfg.Compliance == nil {
		return audit.Requester{}, nil
	}
	name := strings.TrimSpace(requesterName)
	if name == "" {
		return audit.Requester{}, fmt.Errorf("--requester-name is required when compliance is configured")
	}
	email := strings.TrimSpace(requesterEmail)
	if email == "" {
		return audit.Requester{}, fmt.Errorf("--requester-email is required when compliance is configured")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return audit.Requester{}, fmt.Errorf("--requester-email %q is not a valid email address", email)
	}
	return audit.Requester{Name: name, Email: email}, nil
}

// writeAudit assembles and stores the audit record for a completed run. It loads
// the run's manifest, redacts the config, and writes to the configured sink. Any
// failure is returned so the caller can hard-fail the command.
func writeAudit(ctx context.Context, cfg *config.Config, action, runDir string, requester audit.Requester) error {
	man, err := manifest.Load(filepath.Join(runDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}

	runDirAbs, err := filepath.Abs(runDir)
	if err != nil {
		runDirAbs = runDir
	}

	doc, err := audit.Build(audit.BuildInput{
		Action:       action,
		Requester:    requester,
		Config:       cfg,
		Manifest:     man,
		RunName:      filepath.Base(runDir),
		RunDirectory: runDirAbs,
		ToolVersion:  version,
		Now:          time.Now(),
	})
	if err != nil {
		return err
	}

	sink, err := audit.NewSink(cfg.Compliance.Audit)
	if err != nil {
		return err
	}
	return audit.Write(ctx, sink, doc)
}

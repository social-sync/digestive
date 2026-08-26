package cmd

import (
	"fmt"

	"github.com/social-sync/digestive/internal/export"
	"github.com/social-sync/digestive/internal/report"
	"github.com/spf13/cobra"
)

var reportFormat string

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Print an anonymisation report of the configured tables",
	Long: "report connects to the source, then prints a document describing every " +
		"whitelisted table: for each column, whether it is anonymised, excluded, or " +
		"passing through untouched, and which transform is applied. Output goes to " +
		"stdout as a self-contained HTML document or Markdown (--format).",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return doReport(cmd)
	},
}

// doReport validates the config against the live schema (the same strict check
// as `validate`), builds the report model, and prints it in the chosen format.
// It writes the document straight to stdout and ignores --json: the document is
// the command's output.
func doReport(cmd *cobra.Command) error {
	// Resolve the format before any database work so a bad --format fails fast.
	format, err := report.ParseFormat(reportFormat)
	if err != nil {
		return err
	}

	cfg, src, err := loadConfigAndOpen()
	if err != nil {
		return err
	}
	defer src.Close()

	// A stale or invalid config (e.g. a configured column no longer in the
	// schema) fails here rather than producing a misleading report.
	if _, err := export.Validate(cmd.Context(), src, cfg); err != nil {
		return err
	}

	rep, err := report.Build(cmd.Context(), src, cfg)
	if err != nil {
		return err
	}
	fmt.Print(report.Render(rep, format))
	return nil
}

func init() {
	reportCmd.Flags().StringVar(&reportFormat, "format", "html", "output format: html or markdown")
}

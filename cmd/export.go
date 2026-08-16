package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/export"
	"github.com/social-sync/digestive/internal/source"
	"github.com/social-sync/digestive/internal/tui"
	"github.com/spf13/cobra"
)

var (
	runName         string
	deleteOnFailure bool
	noTUI           bool
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export configured tables to Parquet",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, src, err := loadConfigAndOpen()
		if err != nil {
			return err
		}
		defer src.Close()

		runDir, err := runExport(cmd.Context(), src, cfg)
		if err != nil {
			return err
		}
		fmt.Println(runDir)
		return nil
	},
}

// runExport runs the export with a live TUI when stderr is a terminal, and
// otherwise falls back to plain structured logging. The plain path is also
// taken when --no-tui is set or --log-level debug is requested, so diagnostic
// logs are never hidden behind the UI.
func runExport(ctx context.Context, src source.Source, cfg *config.Config) (string, error) {
	if noTUI || logLevel == "debug" || !stderrIsTerminal() {
		return export.Run(ctx, src, cfg, export.Options{
			RunName:         runName,
			Now:             time.Now(),
			DeleteOnFailure: deleteOnFailure,
			Logger:          newLogger(),
		})
	}
	return tui.Run(func(rep export.Reporter) (string, error) {
		return export.Run(ctx, src, cfg, export.Options{
			RunName:         runName,
			Now:             time.Now(),
			DeleteOnFailure: deleteOnFailure,
			Progress:        rep,
		})
	})
}

func init() {
	exportCmd.Flags().StringVar(&runName, "run-name", "", "run directory name (default: timestamp)")
	exportCmd.Flags().BoolVar(&deleteOnFailure, "delete-on-failure", false, "remove the run directory if the export fails")
	exportCmd.Flags().BoolVar(&noTUI, "no-tui", false, "disable the live progress UI and log plainly instead")
	exportCmd.SetContext(context.Background())
}

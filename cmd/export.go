package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/export"
	"github.com/social-sync/digestive/internal/manifest"
	"github.com/social-sync/digestive/internal/source"
	"github.com/social-sync/digestive/internal/tui"
	"github.com/spf13/cobra"
)

var (
	runName         string
	deleteOnFailure bool
	noTUI           bool
)

// tableStat is one table's row count in an export/sync JSON result.
type tableStat struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// exportResult is the --json payload for a successful export.
type exportResult struct {
	RunDir    string      `json:"run_dir"`
	RunID     string      `json:"run_id"`
	Tables    []tableStat `json:"tables"`
	TotalRows int64       `json:"total_rows"`
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export configured tables to Parquet",
	RunE: func(cmd *cobra.Command, _ []string) error {
		res, warnings, err := doExport(cmd.Context())
		return finish("export", res, warnings, err, func() {
			fmt.Println(res.RunDir)
		})
	},
}

// doExport runs the whole export command — plan, export, and (when compliance is
// on) audit — and returns the JSON result payload plus any warnings collected
// during the run. It is the single source of the command's outcome, so both the
// human and --json paths report exactly the same success and failure.
func doExport(ctx context.Context) (*exportResult, []string, error) {
	cfg, src, err := loadConfigAndOpen()
	if err != nil {
		return nil, nil, err
	}
	defer src.Close()

	// Validate the requester before any export work when compliance is on.
	requester, err := requireRequester(cfg)
	if err != nil {
		return nil, nil, err
	}

	runDir, warnings, err := runExport(ctx, src, cfg)
	if err != nil {
		return nil, warnings, err
	}

	// Success-only audit, written after the export completes. A failure here
	// hard-fails the command; with --cleanup-on-audit-fail the run directory
	// is removed so a "successful" export can never exist without its audit.
	if cfg.Compliance != nil {
		if err := writeAudit(ctx, cfg, "export", runDir, requester); err != nil {
			if cleanupOnAuditFail {
				os.RemoveAll(runDir)
			}
			return nil, warnings, err
		}
	}

	res, err := exportResultFromManifest(runDir)
	if err != nil {
		return nil, warnings, err
	}
	return res, warnings, nil
}

// exportResultFromManifest reads back the manifest the run just wrote and turns
// it into the JSON result payload. The manifest is the authoritative record of
// what was exported, so the payload never diverges from the artifact on disk.
func exportResultFromManifest(runDir string) (*exportResult, error) {
	man, err := manifest.Load(filepath.Join(runDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	res := &exportResult{RunDir: runDir, RunID: man.RunID}
	for _, t := range man.Tables {
		res.Tables = append(res.Tables, tableStat{Name: t.Name, Rows: t.Rows})
		res.TotalRows += t.Rows
	}
	return res, nil
}

// runExport runs the export and returns the run directory plus any warnings.
// It uses a live TUI when stderr is a terminal, and otherwise falls back to
// plain structured logging. The plain path is also taken when --no-tui or
// --json is set or --log-level debug is requested, so diagnostic logs are never
// hidden behind the UI and --json output stays pure. Under --json a warning
// collector captures the events the TUI would otherwise surface.
func runExport(ctx context.Context, src source.Source, cfg *config.Config) (string, []string, error) {
	if jsonOutput {
		wc := &warnCollector{}
		dir, err := export.Run(ctx, src, cfg, export.Options{
			RunName:         runName,
			Now:             time.Now(),
			DeleteOnFailure: deleteOnFailure,
			Logger:          newLogger(),
			Progress:        wc,
		})
		return dir, wc.warnings, err
	}
	if noTUI || logLevel == "debug" || !stderrIsTerminal() {
		dir, err := export.Run(ctx, src, cfg, export.Options{
			RunName:         runName,
			Now:             time.Now(),
			DeleteOnFailure: deleteOnFailure,
			Logger:          newLogger(),
		})
		return dir, nil, err
	}
	dir, err := tui.Run(func(rep export.Reporter) (string, error) {
		return export.Run(ctx, src, cfg, export.Options{
			RunName:         runName,
			Now:             time.Now(),
			DeleteOnFailure: deleteOnFailure,
			Progress:        rep,
		})
	})
	return dir, nil, err
}

// warnCollector is an export.Reporter that records only Warn events, for the
// --json path where warnings go into the envelope rather than a live UI.
type warnCollector struct{ warnings []string }

func (*warnCollector) Start(string, string, []string)         {}
func (*warnCollector) TableStart(string)                      {}
func (*warnCollector) TableRows(string, int64)                {}
func (*warnCollector) TableDone(string, int64, time.Duration) {}
func (w *warnCollector) Warn(msg string)                      { w.warnings = append(w.warnings, msg) }
func (*warnCollector) Failed(string, error)                   {}
func (*warnCollector) Done(string, string, time.Duration)     {}

func init() {
	exportCmd.Flags().StringVar(&runName, "run-name", "", "run directory name (default: timestamp)")
	exportCmd.Flags().BoolVar(&deleteOnFailure, "delete-on-failure", false, "remove the run directory if the export fails")
	exportCmd.Flags().BoolVar(&noTUI, "no-tui", false, "disable the live progress UI and log plainly instead")
	addComplianceFlags(exportCmd)
	exportCmd.SetContext(context.Background())
}

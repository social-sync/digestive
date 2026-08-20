package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/restore"
	"github.com/social-sync/digestive/internal/source"
	"github.com/social-sync/digestive/internal/target"
	"github.com/spf13/cobra"
)

var (
	syncYes             bool
	syncCleanup         bool
	syncDialect         string
	syncBatchSize       int
	syncAllowIncomplete bool
	syncIgnoreConf      bool
)

var syncCmd = &cobra.Command{
	Use:   "sync [run-dir]",
	Short: "Export and apply straight into a destination database",
	Long: "sync runs the whole export→restore→apply pipeline: it exports the " +
		"configured tables, generates the same INSERTs `restore` would, and applies " +
		"them directly into the destination database in config's `sync` block — no " +
		"mysql client, no intermediate piping.\n\n" +
		"With no argument it exports fresh, then applies. Given a run directory it " +
		"skips the export and applies that existing run (useful for retrying a failed " +
		"apply without re-querying the source).\n\n" +
		"The whole apply runs in a single transaction: it either lands completely or " +
		"rolls back, leaving the destination untouched. The destination schema must " +
		"already exist — sync inserts data, it does not create tables. A restore.yaml " +
		"in the working directory is honoured exactly as `restore` honours it.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		res, warnings, err := runSync(cmd.Context(), args)
		return finish("sync", res, warnings, err, func() {
			// The (kept) run directory is echoed for inspection or re-apply;
			// a cleaned-up run has none to print.
			if res.RunDir != nil {
				fmt.Println(*res.RunDir)
			}
		})
	},
}

// syncResult is the --json payload for a successful sync.
type syncResult struct {
	RunDir      *string     `json:"run_dir"` // null when --cleanup removed it
	RunID       string      `json:"run_id"`
	Tables      []tableStat `json:"tables"`
	TotalRows   int64       `json:"total_rows"`
	Applied     bool        `json:"applied"`
	Destination syncDest    `json:"destination"`
}

// syncDest identifies the database a sync applied into.
type syncDest struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Database string `json:"database"`
}

func runSync(ctx context.Context, args []string) (*syncResult, []string, error) {
	// A machine consumer cannot answer the confirmation prompt, so --json must
	// carry an explicit --yes: it forces the caller to acknowledge the live-DB
	// write rather than have it silently implied.
	if jsonOutput && !syncYes {
		return nil, nil, fmt.Errorf("--yes is required with --json")
	}

	log := newLogger()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}

	// Validate the requester up front when compliance is on, so a missing flag
	// fails before any target connection or export work.
	requester, err := requireRequester(cfg)
	if err != nil {
		return nil, nil, err
	}

	// Resolve and open the destination first, so a missing/invalid sync block or
	// an unreachable target fails before any export work happens.
	tgt, err := target.Open(cfg.Sync.Type, cfg.Sync.DSN)
	if err != nil {
		return nil, nil, err
	}
	defer tgt.Close()

	dialect := tgt.Dialect()
	if syncDialect != "" {
		dialect, err = restore.ParseDialect(syncDialect)
		if err != nil {
			return nil, nil, err
		}
	}

	if err := tgt.Ping(ctx); err != nil {
		return nil, nil, fmt.Errorf("connect to destination: %w", err)
	}

	// Confirmation guard: this is the one command that writes to a live database,
	// so prompt before doing so — but only when attached to a terminal, so CI and
	// piped runs are never blocked. --yes skips it explicitly.
	if !syncYes && stderrIsTerminal() {
		ok, err := confirmSync(os.Stdin, cfg.Sync.Type, tgt.Host(), tgt.Database())
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, fmt.Errorf("aborted")
		}
	}

	// Obtain a run directory: use the one given, or export a fresh one.
	runDir := ""
	created := false
	var warnings []string
	if len(args) == 1 {
		runDir = args[0]
	} else {
		if cfg.Source.DSN == "" {
			return nil, nil, fmt.Errorf("source.dsn is required to export (or pass an existing run directory)")
		}
		src, err := source.OpenSingleStore(cfg.Source.DSN)
		if err != nil {
			return nil, nil, err
		}
		runDir, warnings, err = runExport(ctx, src, cfg)
		src.Close()
		if err != nil {
			return nil, warnings, err
		}
		created = true
	}

	// Write the audit record after the export artifact exists but before any data
	// is applied downstream, so a "data loaded" state can never coexist with a
	// missing audit trail. Reusing an existing run directory still logs a "sync".
	if cfg.Compliance != nil {
		if err := writeAudit(ctx, cfg, "sync", runDir, requester); err != nil {
			if created && cleanupOnAuditFail {
				os.RemoveAll(runDir)
			}
			return nil, warnings, err
		}
	}

	// Build the result payload from the manifest before any cleanup, so the
	// table/row summary survives even when --cleanup removes the directory.
	res, err := exportResultFromManifest(runDir)
	if err != nil {
		return nil, warnings, err
	}

	// Prepare the restore, discovering restore.yaml exactly as `restore` does.
	rulesPath := ""
	if !syncIgnoreConf {
		if _, err := os.Stat(restore.DefaultRulesFile); err == nil {
			rulesPath = restore.DefaultRulesFile
		}
	}
	prepared, err := restore.Prepare(restore.Options{
		RunDir:          runDir,
		Dialect:         dialect,
		BatchSize:       syncBatchSize,
		AllowIncomplete: syncAllowIncomplete,
		RulesPath:       rulesPath,
		Logger:          log,
	})
	if err != nil {
		return nil, warnings, err
	}

	log.Info("applying to destination", "host", tgt.Host(), "database", tgt.Database(), "type", cfg.Sync.Type)
	if err := tgt.Apply(ctx, prepared, &applyLogger{log: log}); err != nil {
		return nil, warnings, err
	}
	log.Info("sync complete", "host", tgt.Host(), "database", tgt.Database())

	dir := runDir
	result := &syncResult{
		RunDir:      &dir,
		RunID:       res.RunID,
		Tables:      res.Tables,
		TotalRows:   res.TotalRows,
		Applied:     true,
		Destination: syncDest{Type: cfg.Sync.Type, Host: tgt.Host(), Database: tgt.Database()},
	}

	// Clean up the run directory only when we created it this run and were asked
	// to; a run directory passed in is never ours to delete, and a failed apply
	// (returned above) always keeps it for inspection and retry.
	if created && syncCleanup {
		if err := os.RemoveAll(runDir); err != nil {
			log.Warn("cleanup failed", "dir", runDir, "err", err)
		} else {
			log.Info("run directory removed", "dir", runDir)
			result.RunDir = nil
		}
	}

	return result, warnings, nil
}

// confirmSync prompts on stderr for confirmation before writing to a live
// database and reads a yes/no answer from in. Anything other than y/yes aborts.
func confirmSync(in io.Reader, typ, host, db string) (bool, error) {
	fmt.Fprintf(os.Stderr, "About to sync into %s database %q on %s.\n", typ, db, host)
	fmt.Fprint(os.Stderr, "This INSERTs into existing tables in a single transaction. Continue? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// applyLogger adapts the slog logger to target.Reporter for the apply phase.
// The export phase already reports via the TUI or plain logging; the apply
// phase logs each table as it lands.
type applyLogger struct{ log *slog.Logger }

func (a *applyLogger) TableStart(table string) { a.log.Debug("applying table", "table", table) }
func (a *applyLogger) TableDone(table string, rows int64) {
	a.log.Info("table applied", "table", table, "rows", rows)
}

func init() {
	syncCmd.Flags().BoolVar(&syncYes, "yes", false, "skip the confirmation prompt")
	syncCmd.Flags().BoolVar(&syncCleanup, "cleanup", false, "delete the run directory after a successful apply (ignored when a run directory is given)")
	syncCmd.Flags().StringVar(&syncDialect, "dialect", "", "override the restore dialect from sync.type (singlestore or mysql)")
	syncCmd.Flags().IntVar(&syncBatchSize, "batch-size", 1000, "rows per multi-row INSERT statement")
	syncCmd.Flags().BoolVar(&syncAllowIncomplete, "allow-incomplete", false, "apply even if the manifest reports an incomplete export")
	syncCmd.Flags().BoolVar(&syncIgnoreConf, "ignore-restore-conf", false, "ignore a restore.yaml in the working directory")
	syncCmd.Flags().BoolVar(&noTUI, "no-tui", false, "disable the live progress UI and log plainly instead")
	addComplianceFlags(syncCmd)
	syncCmd.SetContext(context.Background())
}

package cmd

import (
	"fmt"
	"os"

	"github.com/social-sync/digestive/internal/restore"
	"github.com/spf13/cobra"
)

var (
	restoreDialect         string
	restoreBatchSize       int
	restoreAllowIncomplete bool
	restoreIgnoreConf      bool
)

// restoreResult is the --json payload for restore. Under --json restore reports
// what it would emit rather than the SQL itself: the raw SQL script is produced
// only on the plain (non-JSON) path.
type restoreResult struct {
	RunDir          string              `json:"run_dir"`
	Dialect         string              `json:"dialect"`
	Tables          []restore.TableStat `json:"tables"`
	TotalStatements int                 `json:"total_statements"`
}

var restoreCmd = &cobra.Command{
	Use:   "restore <run-dir>",
	Short: "Turn an export run into a SQL script of INSERTs",
	Long: "restore reads an export run directory (a manifest.json plus one Parquet " +
		"file per table) and writes a single SQL script of INSERT statements to stdout, " +
		"ready to pipe into the mysql client or paste into a SQL editor. It connects to " +
		"nothing and needs no config: the manifest and Parquet files are the only inputs.\n\n" +
		"Types are preserved for a same-engine round-trip. Table names are emitted " +
		"unqualified, so choose the target database with the client (e.g. mysql -D dbname).\n\n" +
		"If a restore.yaml exists in the working directory, its schema-reconciliation " +
		"rules (rename/drop/add columns, rename/drop tables) are applied to the emitted " +
		"SQL, and a line noting this is written to stderr. Pass --ignore-restore-conf to " +
		"skip it.\n\n" +
		"With --json, restore emits no SQL: stdout carries a JSON summary of the run " +
		"(dialect and per-table row/statement counts) instead.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dialect, err := restore.ParseDialect(restoreDialect)
		if err != nil {
			if jsonOutput {
				return reportJSON("restore", nil, nil, err)
			}
			return err
		}

		// Auto-discover restore.yaml in the working directory (where the local
		// app and its migrations live), unless explicitly ignored.
		rulesPath := ""
		if !restoreIgnoreConf {
			if _, err := os.Stat(restore.DefaultRulesFile); err == nil {
				rulesPath = restore.DefaultRulesFile
			}
		}

		if jsonOutput {
			res, err := restoreSummary(args[0], dialect, rulesPath)
			return reportJSON("restore", res, nil, err)
		}

		return restore.Run(restore.Options{
			RunDir:          args[0],
			Dialect:         dialect,
			BatchSize:       restoreBatchSize,
			AllowIncomplete: restoreAllowIncomplete,
			RulesPath:       rulesPath,
			Out:             os.Stdout,
			Logger:          newLogger(),
		})
	},
}

// restoreSummary prepares the restore and returns a JSON summary of what it
// would emit, without generating any SQL.
func restoreSummary(runDir string, dialect restore.Dialect, rulesPath string) (*restoreResult, error) {
	p, err := restore.Prepare(restore.Options{
		RunDir:          runDir,
		Dialect:         dialect,
		BatchSize:       restoreBatchSize,
		AllowIncomplete: restoreAllowIncomplete,
		RulesPath:       rulesPath,
		Logger:          newLogger(),
	})
	if err != nil {
		return nil, err
	}
	stats := p.Summary()
	res := &restoreResult{
		RunDir:  runDir,
		Dialect: string(dialect),
		Tables:  stats,
	}
	for _, s := range stats {
		res.TotalStatements += s.Statements
	}
	return res, nil
}

func init() {
	restoreCmd.Flags().StringVar(&restoreDialect, "dialect", "", "target SQL engine: singlestore or mysql (required)")
	restoreCmd.Flags().IntVar(&restoreBatchSize, "batch-size", 1000, "rows per multi-row INSERT statement")
	restoreCmd.Flags().BoolVar(&restoreAllowIncomplete, "allow-incomplete", false, "restore even if the manifest reports an incomplete export")
	restoreCmd.Flags().BoolVar(&restoreIgnoreConf, "ignore-restore-conf", false, "ignore a restore.yaml in the working directory")
	if err := restoreCmd.MarkFlagRequired("dialect"); err != nil {
		panic(fmt.Sprintf("mark dialect required: %v", err))
	}
}

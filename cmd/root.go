// Package cmd implements the digestive command-line interface.
package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/source"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	cfgPath  string
	logLevel string
)

// Build metadata, injected via -ldflags at release time by GoReleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "digestive",
	Short: "Export and anonymise database tables to Parquet",
	Long: "digestive pulls tables from a SingleStore (MySQL-wire) database, " +
		"applies redaction and deterministic-hashing transforms, and writes the " +
		"results to Parquet files plus a manifest for later reconstruction.",
	Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// The outcome was already emitted as a JSON envelope on stdout; exit
		// non-zero without a stderr line so the --json contract holds.
		if errors.Is(err, errJSONReported) {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "config.yaml", "path to the YAML config file")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "emit a single JSON result on stdout and disable the TUI (quiet unless --log-level is raised)")
	// Record whether --log-level was set explicitly, so --json can stay quiet
	// by default while still honouring an explicit level.
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		logLevelChanged = cmd.Flags().Changed("log-level")
	}
	rootCmd.AddCommand(exportCmd, validateCmd, restoreCmd, syncCmd)
}

func newLogger() *slog.Logger {
	// Under --json, stderr stays quiet unless the caller raised --log-level
	// explicitly; stdout is reserved for the JSON envelope.
	if jsonOutput && !logLevelChanged {
		return slog.New(slog.DiscardHandler)
	}
	var level slog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// stderrIsTerminal reports whether stderr is an interactive terminal, where a
// live TUI makes sense. It is false when stderr is piped or redirected (CI,
// `2>file`, etc.), so those runs get plain logging.
func stderrIsTerminal() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// loadConfigAndOpen loads the config and opens the source connection. The
// caller is responsible for closing the returned source.
func loadConfigAndOpen() (*config.Config, *source.SingleStore, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	if cfg.Source.DSN == "" {
		return nil, nil, fmt.Errorf("source.dsn is required")
	}
	src, err := source.OpenSingleStore(cfg.Source.DSN)
	if err != nil {
		return nil, nil, err
	}
	return cfg, src, nil
}

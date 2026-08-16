// Package cmd implements the grimnir command-line interface.
package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/social-sync/grimnir/internal/config"
	"github.com/social-sync/grimnir/internal/source"
	"github.com/spf13/cobra"
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
	Use:   "grimnir",
	Short: "Export and anonymise database tables to Parquet",
	Long: "grimnir pulls tables from a SingleStore (MySQL-wire) database, " +
		"applies redaction and deterministic-hashing transforms, and writes the " +
		"results to Parquet files plus a manifest for later reconstruction.",
	Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "config.yaml", "path to the YAML config file")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.AddCommand(exportCmd, validateCmd, restoreCmd)
}

func newLogger() *slog.Logger {
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

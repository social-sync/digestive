package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/danmatthews/sql-exporter/internal/export"
	"github.com/spf13/cobra"
)

var (
	runName         string
	deleteOnFailure bool
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

		runDir, err := export.Run(cmd.Context(), src, cfg, export.Options{
			RunName:         runName,
			Now:             time.Now(),
			DeleteOnFailure: deleteOnFailure,
			Logger:          newLogger(),
		})
		if err != nil {
			return err
		}
		fmt.Println(runDir)
		return nil
	},
}

func init() {
	exportCmd.Flags().StringVar(&runName, "run-name", "", "run directory name (default: timestamp)")
	exportCmd.Flags().BoolVar(&deleteOnFailure, "delete-on-failure", false, "remove the run directory if the export fails")
	exportCmd.SetContext(context.Background())
}

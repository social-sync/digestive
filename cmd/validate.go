package cmd

import (
	"fmt"

	"github.com/social-sync/digestive/internal/export"
	"github.com/spf13/cobra"
)

// validateResult is the --json payload for a successful validate.
type validateResult struct {
	Tables     []string `json:"tables"`
	TableCount int      `json:"table_count"`
}

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Check the config against the live schema without exporting",
	RunE: func(cmd *cobra.Command, _ []string) error {
		res, err := doValidate(cmd)
		return finish("validate", res, nil, err, func() {
			fmt.Println("config is valid")
		})
	},
}

func doValidate(cmd *cobra.Command) (*validateResult, error) {
	cfg, src, err := loadConfigAndOpen()
	if err != nil {
		return nil, err
	}
	defer src.Close()

	tables, err := export.Validate(cmd.Context(), src, cfg)
	if err != nil {
		return nil, err
	}
	return &validateResult{Tables: tables, TableCount: len(tables)}, nil
}

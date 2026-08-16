package cmd

import (
	"fmt"

	"github.com/social-sync/digestive/internal/export"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Check the config against the live schema without exporting",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, src, err := loadConfigAndOpen()
		if err != nil {
			return err
		}
		defer src.Close()

		if err := export.Validate(cmd.Context(), src, cfg); err != nil {
			return err
		}
		fmt.Println("config is valid")
		return nil
	},
}

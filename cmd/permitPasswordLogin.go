package cmd

import (
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/spf13/cobra"
)

var PermitPasswordLoginCmd = &cobra.Command{
	Use:   "permit-login",
	Short: "Force permit password login",
	Long:  `Force permit password login`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withOfflineDatabase(func() error {
			if err := config.Set(config.DisablePasswordLoginKey, false); err != nil {
				return err
			}
			cmd.Println("Password login has been permitted.")
			cmd.Println("Please restart the server to apply the changes.")
			return nil
		})
	},
}

func init() {
	RootCmd.AddCommand(PermitPasswordLoginCmd)
}

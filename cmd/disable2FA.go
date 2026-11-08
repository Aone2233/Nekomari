package cmd

import (
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/spf13/cobra"
	"gorm.io/gorm"
)

var Disable2FA = &cobra.Command{
	Use:   "disable-2fa",
	Short: "Force disable 2FA",
	Long:  `Force disable 2FA`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withOfflineDatabase(func() error {
			db := dbcore.GetDBInstance()
			if err := db.Transaction(func(tx *gorm.DB) error {
				return tx.Model(&models.User{}).Where("two_factor != ?", "").
					Update("two_factor", "").Error
			}); err != nil {
				return err
			}
			cmd.Println("2FA has been disabled.")
			cmd.Println("Please restart the server to apply the changes.")
			return nil
		})
	},
}

func init() {
	RootCmd.AddCommand(Disable2FA)
}

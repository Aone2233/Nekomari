package cmd

import (
	"fmt"
	"os"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/spf13/cobra"
)

var (
	//Username    string
	NewPassword string
)

var ChpasswdCmd = &cobra.Command{
	Use:     "chpasswd",
	Short:   "Force change password",
	Long:    `Force change password`,
	Example: `komari chpasswd -p <password>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if NewPassword == "" {
			return cmd.Help()
		}
		if _, err := os.Stat(flags.DatabaseFile); os.IsNotExist(err) {
			return fmt.Errorf("database file does not exist")
		}
		return withOfflineDatabase(func() error {
			user := &models.User{}
			if err := dbcore.GetDBInstance().Model(&models.User{}).First(user).Error; err != nil {
				return err
			}
			cmd.Println("Changing password for user:", user.Username)
			if err := accounts.ForceResetPassword(user.Username, NewPassword); err != nil {
				return err
			}
			if err := accounts.DeleteAllSessions(); err != nil {
				return fmt.Errorf("password changed, but unable to force logout of other devices: %w", err)
			}
			cmd.Println("Password changed successfully.")
			cmd.Println("Please restart the server to apply the changes.")
			return nil
		})
	},
}

func init() {
	//ChpasswdCmd.PersistentFlags().StringVarP(&Username, "user", "u", "admin", "The username of the account to change password")
	ChpasswdCmd.PersistentFlags().StringVarP(&NewPassword, "password", "p", "", "New password")
	RootCmd.AddCommand(ChpasswdCmd)
}

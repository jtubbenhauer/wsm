package cmd

import (
	"fmt"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:     "remove <name>",
	Short:   "Deregister a workspace",
	Aliases: []string{"rm"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()

		if err := store.RemoveWorkspace(name); err != nil {
			return err
		}

		fmt.Printf("Removed workspace %q\n", name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}

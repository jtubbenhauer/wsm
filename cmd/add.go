package cmd

import (
	"fmt"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add <name> <path>",
	Short: "Register a repository as a workspace",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		path := args[1]

		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()

		ws, err := store.AddWorkspace(name, path, db.WorkspaceTypeRepo, "", "", nil)
		if err != nil {
			return err
		}

		fmt.Printf("Added workspace %q at %s\n", ws.Name, ws.Path)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(addCmd)
}

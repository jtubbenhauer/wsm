package cmd

import (
	"fmt"
	"strings"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/jacksteamdev/wsm/internal/git"
	"github.com/spf13/cobra"
)

var (
	workspaceBase     string
	workspaceSymlinks string
)

var defaultSymlinks = []string{}

var workspaceCmd = &cobra.Command{
	Use:   "workspace <parent-name> <branch>",
	Short: "Create a git worktree from a workspace and register it",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		parentName := args[0]
		branch := args[1]

		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()

		parent, err := store.GetWorkspace(parentName)
		if err != nil {
			return err
		}
		if parent == nil {
			return fmt.Errorf("workspace %q not found", parentName)
		}

		symlinks := defaultSymlinks
		if workspaceSymlinks != "" {
			symlinks = strings.Split(workspaceSymlinks, ",")
		}

		wtPath, err := git.CreateWorktree(parent.Path, branch, workspaceBase)
		if err != nil {
			return err
		}

		if err := git.CreateSymlinks(parent.Path, wtPath, symlinks); err != nil {
			fmt.Printf("warning: some symlinks failed: %v\n", err)
		}

		wtName := fmt.Sprintf("%s/%s", parentName, git.SanitiseBranchName(branch))
		ws, err := store.AddWorkspace(wtName, wtPath, db.WorkspaceTypeWorktree, parent.Path, branch, symlinks)
		if err != nil {
			return err
		}

		fmt.Printf("Created worktree %q at %s\n", ws.Name, ws.Path)
		return nil
	},
}

func init() {
	workspaceCmd.Flags().StringVar(&workspaceBase, "base", "", "Base ref to create branch from")
	workspaceCmd.Flags().StringVar(&workspaceSymlinks, "symlinks", "", "Comma-separated list of symlinks to create in the worktree")
	rootCmd.AddCommand(workspaceCmd)
}

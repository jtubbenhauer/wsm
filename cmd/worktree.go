package cmd

import (
	"fmt"
	"strings"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/jacksteamdev/wsm/internal/git"
	"github.com/spf13/cobra"
)

var (
	worktreeBase     string
	worktreeSymlinks string
)

var defaultSymlinks = []string{"node_modules", ".env", ".env.local"}

var worktreeCmd = &cobra.Command{
	Use:   "worktree <parent-name> <branch>",
	Short: "Create a git worktree and register as workspace",
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
		if worktreeSymlinks != "" {
			symlinks = strings.Split(worktreeSymlinks, ",")
		}

		wtPath, err := git.CreateWorktree(parent.Path, branch, worktreeBase)
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
	worktreeCmd.Flags().StringVar(&worktreeBase, "base", "", "Base ref to create branch from")
	worktreeCmd.Flags().StringVar(&worktreeSymlinks, "symlinks", "", "Comma-separated list of symlinks (default: node_modules,.env,.env.local)")
	rootCmd.AddCommand(worktreeCmd)
}

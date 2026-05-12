package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/jacksteamdev/wsm/internal/git"
	"github.com/spf13/cobra"
)

var scanCmd = &cobra.Command{
	Use:   "scan [dir]",
	Short: "Auto-discover git repos and register as workspaces",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scanDir := ""
		if len(args) > 0 {
			scanDir = args[0]
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("getting home dir: %w", err)
			}
			scanDir = filepath.Join(home, "dev")
		}

		absDir, err := filepath.Abs(scanDir)
		if err != nil {
			return fmt.Errorf("resolving path: %w", err)
		}

		repos, err := git.ScanForRepos(absDir)
		if err != nil {
			return err
		}

		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()

		var added, skipped int
		for _, repo := range repos {
			// Skip worktrees — we only register parent repos now.
			// Existing worktree workspaces in the DB remain functional.
			if repo.IsWorktree {
				skipped++
				continue
			}

			existing, err := store.GetWorkspaceByPath(repo.Path)
			if err != nil {
				return err
			}
			if existing != nil {
				skipped++
				continue
			}

			existingByName, err := store.GetWorkspace(repo.Name)
			if err != nil {
				return err
			}
			if existingByName != nil {
				skipped++
				continue
			}

			_, err = store.AddWorkspace(repo.Name, repo.Path, db.WorkspaceTypeRepo, repo.ParentPath, repo.Branch, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", repo.Name, err)
				skipped++
				continue
			}
			added++
			fmt.Printf("  + %s (%s) [repo]\n", repo.Name, repo.Path)
		}

		// Remove workspaces whose paths no longer point to a valid git repo
		existing, err := store.ListWorkspaces()
		if err != nil {
			return fmt.Errorf("listing workspaces for cleanup: %w", err)
		}
		var removed int
		for _, ws := range existing {
			if !git.IsGitRepo(ws.Path) {
				if err := store.RemoveWorkspace(ws.Name); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", ws.Name, err)
					continue
				}
				removed++
				fmt.Printf("  - %s (%s)\n", ws.Name, ws.Path)
			}
		}

		fmt.Printf("\nScanned %s: %d added, %d removed, %d unchanged\n", absDir, added, removed, skipped)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(scanCmd)
}

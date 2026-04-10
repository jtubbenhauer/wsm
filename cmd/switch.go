package cmd

import (
	"fmt"
	"os"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/jacksteamdev/wsm/internal/git"
	"github.com/jacksteamdev/wsm/internal/picker"
	"github.com/jacksteamdev/wsm/internal/tmux"
	"github.com/spf13/cobra"
)

var switchCmd = &cobra.Command{
	Use:   "switch",
	Short: "Quick-switch between active workspace sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()

		workspaces, err := store.ListWorkspaces()
		if err != nil {
			return err
		}

		for {
			activeSessions := tmux.ListSessions()
			if len(activeSessions) == 0 {
				fmt.Println("No active tmux sessions.")
				return nil
			}

			sessionSet := make(map[string]bool, len(activeSessions))
			for _, s := range activeSessions {
				sessionSet[s] = true
			}

			var activeWorkspaces []db.Workspace
			for _, ws := range workspaces {
				if sessionSet[tmux.SanitiseName(ws.Name)] {
					activeWorkspaces = append(activeWorkspaces, ws)
				}
			}

			if len(activeWorkspaces) == 0 {
				fmt.Println("No active workspace sessions.")
				return nil
			}

			branches := make(map[string]string, len(activeWorkspaces))
			for _, ws := range activeWorkspaces {
				if b, err := git.CurrentBranch(ws.Path); err == nil {
					branches[ws.Name] = b
				}
			}

			result, err := picker.RunSwitchPicker(activeWorkspaces, branches)
			if err != nil {
				return fmt.Errorf("running switch picker: %w", err)
			}
			if result == nil {
				return nil
			}

			if result.KillRequest {
				if err := tmux.KillSession(result.Workspace.Name); err != nil {
					fmt.Fprintf(os.Stderr, "failed to kill session: %v\n", err)
				} else {
					fmt.Printf("Killed session: %s\n", result.Workspace.Name)
				}
				continue
			}

			name := tmux.SanitiseName(result.Workspace.Name)
			if err := tmux.SwitchClient(name); err != nil {
				return fmt.Errorf("switching to session: %w", err)
			}
			return tmux.SelectWindow(name, 2)
		}
	},
}

func init() {
	rootCmd.AddCommand(switchCmd)
}

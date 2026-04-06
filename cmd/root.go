package cmd

import (
	"fmt"
	"os"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/jacksteamdev/wsm/internal/opencode"
	"github.com/jacksteamdev/wsm/internal/picker"
	"github.com/jacksteamdev/wsm/internal/tmux"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "wsm",
	Short: "Workspace Session Manager — tmux-based opencode session picker",
	Long:  "A lightweight CLI tool that provides a global tmux-based picker for managing opencode sessions across workspaces.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPicker()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runPicker() error {
	store, err := db.Open()
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer store.Close()

	workspaces, err := store.ListWorkspaces()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		fmt.Println("No workspaces registered. Run 'wsm scan' or 'wsm add <name> <path>' first.")
		return nil
	}

	client, err := opencode.EnsureServer(opencode.DefaultHost, opencode.DefaultPort)
	if err != nil {
		return fmt.Errorf("ensuring opencode server: %w", err)
	}

	dirs := make([]string, len(workspaces))
	for i, ws := range workspaces {
		dirs[i] = ws.Path
	}

	for {
		sessionsByDir, err := client.FetchSessionsForDirs(dirs)
		if err != nil {
			return fmt.Errorf("fetching sessions: %w", err)
		}

		statuses := client.FetchStatusesForDirs(dirs)

		items := picker.BuildPickerItems(workspaces, sessionsByDir, statuses)

		result, err := picker.RunFzf(items)
		if err != nil {
			return fmt.Errorf("running picker: %w", err)
		}
		if result == nil {
			return nil // user cancelled
		}

		if result.DeleteRequest {
			if err := client.DeleteSession(result.Item.SessionID); err != nil {
				return fmt.Errorf("deleting session: %w", err)
			}
			fmt.Printf("Deleted session: %s\n", result.Item.SessionTitle)
			continue
		}

		var selected *picker.PickerItem

		if result.NewRequest {
			// Ctrl-N pressed: show workspace sub-picker
			ws, err := picker.RunWorkspacePicker(workspaces)
			if err != nil {
				return fmt.Errorf("running workspace picker: %w", err)
			}
			if ws == nil {
				return nil
			}
			selected = &picker.PickerItem{
				WorkspaceName: ws.Name,
				WorkspacePath: ws.Path,
				IsNew:         true,
			}
		} else {
			selected = result.Item
		}

		sessionID := selected.SessionID
		if selected.IsNew {
			session, err := client.CreateSession(selected.WorkspacePath)
			if err != nil {
				return fmt.Errorf("creating session: %w", err)
			}
			sessionID = session.ID
			fmt.Printf("Created new session for %s\n", selected.WorkspaceName)
		}

		// Track activity
		ws, err := store.GetWorkspace(selected.WorkspaceName)
		if err == nil && ws != nil {
			store.UpsertSessionActivity(ws.ID, sessionID, selected.SessionTitle)
		}

		layout := tmux.SessionLayout{
			Name:          selected.WorkspaceName,
			WorkspacePath: selected.WorkspacePath,
			SessionID:     sessionID,
		}

		if err := tmux.CreateWorkspaceSession(layout); err != nil {
			return fmt.Errorf("creating tmux session: %w", err)
		}

		return tmux.SwitchOrAttach(selected.WorkspaceName)
	}
}

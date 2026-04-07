package cmd

import (
	"fmt"
	"os"
	"sync"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/jacksteamdev/wsm/internal/opencode"
	"github.com/jacksteamdev/wsm/internal/picker"
	"github.com/jacksteamdev/wsm/internal/plans"
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

	var filterWorkspace *db.Workspace
	var items []picker.PickerItem

	for {
		if items == nil {
			sessionsByDir, statuses, err := fetchAll(client, dirs)
			if err != nil {
				return fmt.Errorf("fetching sessions: %w", err)
			}
			labels, _ := store.GetSessionLabels()
			if labels == nil {
				labels = make(map[string]string)
			}
			items = picker.BuildPickerItems(workspaces, sessionsByDir, statuses, labels)
		}

		activeFilter := ""
		displayItems := items
		if filterWorkspace != nil {
			activeFilter = filterWorkspace.Name
			displayItems = picker.FilterItemsByWorkspace(items, filterWorkspace.Name)
		}

		result, err := picker.RunFzf(displayItems, activeFilter)
		if err != nil {
			return fmt.Errorf("running picker: %w", err)
		}
		if result == nil {
			return nil // user cancelled
		}

		if result.WorkspaceFilter {
			ws, err := picker.RunWorkspacePicker(workspaces, true)
			if err != nil {
				return fmt.Errorf("running workspace filter picker: %w", err)
			}
			if ws == nil {
				continue // user cancelled sub-picker, re-show main picker
			}
			if ws.Name == picker.AllWorkspacesName {
				filterWorkspace = nil
			} else {
				filterWorkspace = ws
			}
			// reuse cached items — no re-fetch needed
			continue
		}

		if result.PlanRequest {
			if err := openPlanForWorkspace(result.Item.WorkspacePath, result.Item.WorkspaceName); err != nil {
				fmt.Fprintf(os.Stderr, "plan viewer: %v\n", err)
			}
			continue
		}

		// any action other than filter change invalidates the cache
		items = nil

		if result.DeleteRequest {
			if err := client.DeleteSession(result.Item.SessionID); err != nil {
				return fmt.Errorf("deleting session: %w", err)
			}
			tmux.CleanupParkedPane(result.Item.WorkspaceName, result.Item.SessionID)
			fmt.Printf("Deleted session: %s\n", result.Item.SessionTitle)
			continue
		}

		if result.RenameRequest {
			newLabel, err := picker.RunRenamePrompt(result.Item.SessionTitle)
			if err != nil {
				return fmt.Errorf("running rename prompt: %w", err)
			}
			if newLabel == "" || newLabel == result.Item.SessionTitle {
				continue
			}
			ws, err := store.GetWorkspace(result.Item.WorkspaceName)
			if err != nil {
				return fmt.Errorf("getting workspace: %w", err)
			}
			if ws == nil {
				fmt.Fprintf(os.Stderr, "workspace %q not found\n", result.Item.WorkspaceName)
				continue
			}
			// Ensure session_activity row exists before updating label
			if err := store.UpsertSessionActivity(ws.ID, result.Item.SessionID, newLabel); err != nil {
				return fmt.Errorf("renaming session: %w", err)
			}
			fmt.Printf("Renamed session to: %s\n", newLabel)
			continue
		}

		var selected *picker.PickerItem

		if result.NewRequest {
			// Ctrl-N pressed: show workspace sub-picker
			ws, err := picker.RunWorkspacePicker(workspaces, false)
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

func fetchAll(client *opencode.Client, dirs []string) (picker.SessionsByDir, map[string]opencode.SessionStatus, error) {
	var sessionsByDir picker.SessionsByDir
	var statuses map[string]opencode.SessionStatus
	var sessErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		sessionsByDir, sessErr = client.FetchSessionsForDirs(dirs)
	}()
	go func() {
		defer wg.Done()
		statuses = client.FetchStatusesForDirs(dirs)
	}()

	wg.Wait()

	if sessErr != nil {
		return nil, nil, fmt.Errorf("fetching sessions: %w", sessErr)
	}
	return sessionsByDir, statuses, nil
}

func openPlanForWorkspace(workspacePath, workspaceName string) error {
	files, err := plans.Discover(workspacePath)
	if err != nil {
		return fmt.Errorf("discovering plan files: %w", err)
	}
	if len(files) == 0 {
		fmt.Println("No plan files found in .opencode/plans/")
		return nil
	}

	var target *plans.PlanFile
	if len(files) == 1 {
		target = &files[0]
	} else {
		best := plans.PickBest(files, workspaceName)
		selected, err := picker.RunPlanPicker(files, best)
		if err != nil {
			return fmt.Errorf("running plan picker: %w", err)
		}
		if selected == nil {
			return nil
		}
		target = selected
	}

	return tmux.RunInTerminal("nvim", target.Path)
}

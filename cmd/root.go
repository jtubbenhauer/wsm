package cmd

import (
	"fmt"
	"os"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/jacksteamdev/wsm/internal/git"
	"github.com/jacksteamdev/wsm/internal/pi"
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

	dirs := make([]string, len(workspaces))
	for i, ws := range workspaces {
		dirs[i] = ws.Path
	}

	var filterWorkspace *db.Workspace
	var items []picker.PickerItem

	for {
		if items == nil {
			sessionsByDir, err := pi.FetchSessionsForDirs(dirs)
			if err != nil {
				return fmt.Errorf("fetching sessions: %w", err)
			}
			labels, _ := store.GetSessionLabels()
			if labels == nil {
				labels = make(map[string]string)
			}
			branches, _ := store.GetSessionBranches()
			if branches == nil {
				branches = make(map[string]string)
			}
			items = picker.BuildPickerItems(workspaces, sessionsByDir, labels, branches)
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

		if result.KillRequest {
			activeSessions := tmux.ListSessions()
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
			ws, err := picker.RunKillSessionPicker(activeWorkspaces)
			if err != nil {
				return fmt.Errorf("running kill session picker: %w", err)
			}
			if ws != nil {
				if err := tmux.KillSession(ws.Name); err != nil {
					fmt.Fprintf(os.Stderr, "failed to kill session: %v\n", err)
				} else {
					fmt.Printf("Killed session: %s\n", ws.Name)
				}
			}
			items = nil
			continue
		}

		// any action other than filter change invalidates the cache
		items = nil

		if result.DeleteRequest {
			if err := pi.DeleteSession(result.Item.WorkspacePath, result.Item.SessionID); err != nil {
				return fmt.Errorf("deleting session: %w", err)
			}
			fmt.Printf("Deleted session: %s\n", result.Item.SessionTitle)
			continue
		}

		if result.RenameRequest {
			newLabel, err := tmux.PromptViaNvim("Rename session", result.Item.SessionTitle)
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

			// Get current branch as default for the prompt
			currentBranch, _ := git.CurrentBranch(ws.Path)
			if currentBranch == "" {
				currentBranch = "main"
			}

			branch, err := tmux.PromptViaNvim("Branch name (Enter = current)", currentBranch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "branch prompt: %v\n", err)
				continue
			}
			if branch == "" {
				continue // user cancelled
			}

			// Safe checkout the branch (stash if dirty)
			if ws.Type != db.WorkspaceTypeWorktree {
				stashed, err := git.SafeCheckout(ws.Path, branch)
				if err != nil {
					fmt.Fprintf(os.Stderr, "checkout failed: %v\n", err)
					if stashed {
						fmt.Fprintln(os.Stderr, "Note: working tree was stashed before checkout; check for conflicts")
					}
					continue
				}
			}

			name, err := tmux.PromptViaNvim("Session name", "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "name prompt: %v\n", err)
				continue
			}
			if name == "" {
				continue // user cancelled
			}

			selected = &picker.PickerItem{
				WorkspaceName: ws.Name,
				WorkspacePath: ws.Path,
				SessionTitle:  name,
				Branch:        branch,
				IsNew:         true,
			}
		} else {
			selected = result.Item
		}

		sessionID := selected.SessionID
		var sessionFilePath string
		if selected.IsNew {
			newSession, err := pi.CreateSession(selected.WorkspacePath)
			if err != nil {
				return fmt.Errorf("creating pi session: %w", err)
			}
			sessionID = newSession.ID
			sessionFilePath = newSession.FilePath
		} else if sessionID != "" {
			if path, err := pi.FindSessionFile(selected.WorkspacePath, sessionID); err == nil {
				sessionFilePath = path
			}
		}

		ws, err := store.GetWorkspace(selected.WorkspaceName)
		if err == nil && ws != nil && sessionID != "" {
			store.UpsertSessionActivityWithBranch(ws.ID, sessionID, selected.SessionTitle, selected.Branch)
		}

		// For existing sessions on repo workspaces, checkout the associated branch
		if !selected.IsNew && ws != nil && ws.Type != db.WorkspaceTypeWorktree && selected.Branch != "" {
			stashed, err := git.SafeCheckout(ws.Path, selected.Branch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "checkout failed: %v\n", err)
				if stashed {
					fmt.Fprintln(os.Stderr, "Note: working tree was stashed before checkout; check for conflicts")
				}
				// Don't abort — still try to attach to the session
			}
		}

		layout := tmux.SessionLayout{
			Name:            selected.WorkspaceName,
			WorkspacePath:   selected.WorkspacePath,
			SessionID:       sessionID,
			SessionFilePath: sessionFilePath,
		}

		if err := tmux.CreateWorkspaceSession(layout); err != nil {
			return fmt.Errorf("creating tmux session: %w", err)
		}

		return tmux.SwitchOrAttach(selected.WorkspaceName)
	}
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

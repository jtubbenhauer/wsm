package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/jacksteamdev/wsm/internal/picker"
	"github.com/jacksteamdev/wsm/internal/plans"
	"github.com/jacksteamdev/wsm/internal/tmux"
	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Open plan files from .opencode/plans in nvim",
	Long:  "Discovers markdown plan files in the workspace's .opencode/plans/ directory and opens them in nvim. When inside tmux, uses a floating popup.",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("dir")
		name, _ := cmd.Flags().GetString("name")

		if dir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}
			dir = cwd
		}

		if name == "" {
			name = filepath.Base(dir)
		}

		files, err := plans.Discover(dir)
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
			best := plans.PickBest(files, name)
			selected, err := picker.RunPlanPicker(files, best)
			if err != nil {
				return fmt.Errorf("running plan picker: %w", err)
			}
			if selected == nil {
				return nil
			}
			target = selected
		}

		if tmux.IsInsideTmux() {
			return tmux.DisplayPopup(dir, "nvim", target.Path)
		}

		bin, err := findNvim()
		if err != nil {
			return err
		}
		return syscall.Exec(bin, []string{"nvim", target.Path}, os.Environ())
	},
}

func findNvim() (string, error) {
	for _, name := range []string{"nvim", "vim", "vi"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no editor found (tried nvim, vim, vi)")
}

func init() {
	planCmd.Flags().String("dir", "", "Workspace directory (defaults to current directory)")
	planCmd.Flags().String("name", "", "Workspace name for smart plan detection (defaults to directory basename)")
	rootCmd.AddCommand(planCmd)
}

package cmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/jacksteamdev/wsm/internal/opencode"
	"github.com/spf13/cobra"
)

var attachCmd = &cobra.Command{
	Use:   "attach",
	Short: "Ensure opencode server is running, then exec opencode attach",
	Long:  "Self-healing wrapper for tmux panes. Ensures the opencode server is up before replacing the process with opencode attach.",
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID, _ := cmd.Flags().GetString("session")
		directory, _ := cmd.Flags().GetString("dir")

		if sessionID == "" {
			return fmt.Errorf("--session is required")
		}
		if directory == "" {
			return fmt.Errorf("--dir is required")
		}

		client, err := opencode.EnsureServer(opencode.DefaultHost, opencode.DefaultPort)
		if err != nil {
			return fmt.Errorf("ensuring opencode server: %w", err)
		}

		bin := opencode.OpencodeBinary()
		argv := []string{"opencode", "attach", client.BaseURL(), "-s", sessionID, "--dir", directory}

		return syscall.Exec(bin, argv, os.Environ())
	},
}

func init() {
	attachCmd.Flags().StringP("session", "s", "", "OpenCode session ID")
	attachCmd.Flags().String("dir", "", "Workspace directory")
	rootCmd.AddCommand(attachCmd)
}

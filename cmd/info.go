package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/jacksteamdev/wsm/internal/opencode"
	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info <name>",
	Short: "Show workspace details and sessions",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()

		ws, err := store.GetWorkspace(name)
		if err != nil {
			return err
		}
		if ws == nil {
			return fmt.Errorf("workspace %q not found", name)
		}

		fmt.Printf("Name:       %s\n", ws.Name)
		fmt.Printf("Path:       %s\n", ws.Path)
		fmt.Printf("Type:       %s\n", ws.Type)
		if ws.Branch != "" {
			fmt.Printf("Branch:     %s\n", ws.Branch)
		}
		if ws.ParentPath != "" {
			fmt.Printf("Parent:     %s\n", ws.ParentPath)
		}
		fmt.Printf("Created:    %s\n", ws.CreatedAt.Format("2006-01-02 15:04:05"))

		client := opencode.DefaultClient()
		if !client.IsServerRunning() {
			fmt.Println("\nOpencode server not running — session list unavailable")
			return nil
		}

		sessions, err := client.ListTopLevelSessions(ws.Path)
		if err != nil {
			fmt.Printf("\nCould not fetch sessions: %v\n", err)
			return nil
		}

		if len(sessions) == 0 {
			fmt.Println("\nNo sessions found for this workspace")
			return nil
		}

		fmt.Printf("\nSessions (%d):\n", len(sessions))
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "  ID\tTITLE\tUPDATED")
		for _, s := range sessions {
			title := s.Title
			if len(title) > 50 {
				title = title[:47] + "..."
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\n",
				s.ID[:12]+"...",
				title,
				s.UpdatedAt().Format("2006-01-02 15:04"),
			)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(infoCmd)
}

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/spf13/cobra"
)

var listJSON bool

var listCmd = &cobra.Command{
	Use:     "list",
	Short:   "List registered workspaces",
	Aliases: []string{"ls"},
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

		if listJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(workspaces)
		}

		if len(workspaces) == 0 {
			fmt.Println("No workspaces registered. Use 'wsm add' or 'wsm scan' to add workspaces.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tPATH\tTYPE\tBRANCH")
		for _, ws := range workspaces {
			branch := ws.Branch
			if branch == "" {
				branch = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ws.Name, ws.Path, ws.Type, branch)
		}
		return w.Flush()
	},
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(listCmd)
}

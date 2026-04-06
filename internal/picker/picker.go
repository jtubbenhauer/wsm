package picker

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/jacksteamdev/wsm/internal/db"
	"github.com/jacksteamdev/wsm/internal/opencode"
)

const (
	ansiReset     = "\033[0m"
	ansiDim       = "\033[2m"
	ansiDimBright = "\033[38;5;245m" // gray, lighter than dim
	ansiGreen     = "\033[32m"
	ansiYellow    = "\033[33m"
	ansiRed       = "\033[31m"

	AllWorkspacesName = "__all__"
)

var workspaceColours = []string{
	"\033[38;5;114m",         // green
	"\033[38;5;180m",         // gold
	"\033[38;5;139m",         // mauve
	"\033[38;2;88;166;255m",  // gh blue (#58a6ff)
	"\033[38;5;173m",         // salmon
	"\033[38;5;109m",         // teal
	"\033[38;2;188;140;255m", // gh purple (#bc8cff)
	"\033[38;5;149m",         // lime
	"\033[38;5;146m",         // lavender
	"\033[38;2;210;153;34m",  // gh yellow (#d29922)
}

// SessionsByDir is a map of directory path to top-level sessions for that directory
type SessionsByDir = map[string][]opencode.Session

type PickerItem struct {
	WorkspaceName string
	WorkspacePath string
	SessionID     string
	SessionTitle  string
	UpdatedAt     time.Time
	IsNew         bool
	Status        string // "busy", "retry", or "" (idle)
}

func FormatPickerLine(item PickerItem, width int, maxWsWidth int) string {
	width -= 4 // account for fzf pointer and chrome
	if width < 40 {
		width = 40
	}

	wsColour := colourForWorkspace(item.WorkspaceName)
	wsName := shortName(item.WorkspaceName)
	paddedWs := wsColour + wsName + strings.Repeat(" ", maxWsWidth-len(wsName)) + ansiReset

	indicator := statusIndicator(item.Status)
	age := colouredAge(item.UpdatedAt)

	// maxWsWidth + 2(gap) + 2(indicator) + 4(age) + 2(gap)
	maxTitle := width - maxWsWidth - 10
	if maxTitle < 10 {
		maxTitle = 10
	}
	title := truncate(item.SessionTitle, maxTitle)

	return paddedWs + "  " + indicator + age + "  " + title
}

func ParsePickerLine(line string) (sessionID string, isNew bool) {
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) < 2 {
		return "", false
	}
	meta := parts[1]
	if meta == "new" {
		return "", true
	}
	return meta, false
}

func BuildPickerItems(workspaces []db.Workspace, sessionsByDir SessionsByDir, statuses map[string]opencode.SessionStatus) []PickerItem {
	var items []PickerItem

	for _, ws := range workspaces {
		for _, s := range sessionsByDir[ws.Path] {
			status := ""
			if st, ok := statuses[s.ID]; ok {
				status = st.Type
			}
			items = append(items, PickerItem{
				WorkspaceName: ws.Name,
				WorkspacePath: ws.Path,
				SessionID:     s.ID,
				SessionTitle:  s.Title,
				UpdatedAt:     s.UpdatedAt(),
				Status:        status,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].IsNew != items[j].IsNew {
			return !items[i].IsNew
		}
		if items[i].IsNew {
			return items[i].WorkspaceName < items[j].WorkspaceName
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})

	return items
}

type PickerResult struct {
	Item            *PickerItem
	NewRequest      bool
	DeleteRequest   bool
	WorkspaceFilter bool
}

func RunFzf(items []PickerItem, activeFilter string) (*PickerResult, error) {
	width := getTerminalWidth()

	maxWsWidth := 0
	for _, item := range items {
		if n := len(shortName(item.WorkspaceName)); n > maxWsWidth {
			maxWsWidth = n
		}
	}

	var lines []string
	for _, item := range items {
		displayed := FormatPickerLine(item, width, maxWsWidth)
		meta := item.SessionID
		if item.IsNew {
			meta = "new:" + item.WorkspaceName
		}
		lines = append(lines, displayed+"\t"+meta)
	}

	input := strings.Join(lines, "\n")

	header := "  ctrl-n: new session  ·  ctrl-d: delete  ·  ctrl-w: filter workspace  ·  ctrl-c: cancel\n "
	if activeFilter != "" {
		wsColour := colourForWorkspace(activeFilter)
		filterDisplay := shortName(activeFilter)
		header = "  " + wsColour + "[" + filterDisplay + "]" + ansiReset + "  ctrl-n: new  ·  ctrl-d: delete  ·  ctrl-w: change filter  ·  ctrl-c: cancel\n "
	}

	cmd := exec.Command("fzf",
		"--ansi",
		"--no-multi",
		"--delimiter=\t",
		"--with-nth=1",
		"--layout=reverse",
		"--no-separator",
		"--header="+header,
		"--prompt=  ",
		"--pointer=▸",
		"--info=hidden",
		"--color=fg:#e6edf3,fg+:#f0f6fc:bold,bg:#0d1117,bg+:#161b22,hl:#3fb950,hl+:#3fb950:bold,info:#8b949e,prompt:#58a6ff,pointer:#58a6ff,header:#8b949e,gutter:#0d1117,border:#30363d",
		"--preview-window=hidden",
		"--expect=ctrl-n,ctrl-d,ctrl-w",
	)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code == 1 || code == 130 {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("running fzf: %w", err)
	}

	outputLines := strings.SplitN(strings.TrimRight(string(out), "\n"), "\n", 2)
	key := outputLines[0]

	if key == "ctrl-n" {
		return &PickerResult{NewRequest: true}, nil
	}

	if key == "ctrl-w" {
		return &PickerResult{WorkspaceFilter: true}, nil
	}

	if len(outputLines) < 2 || outputLines[1] == "" {
		return nil, nil
	}

	selected := outputLines[1]
	parts := strings.SplitN(selected, "\t", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected fzf output format")
	}

	meta := parts[1]

	if key == "ctrl-d" {
		if strings.HasPrefix(meta, "new:") {
			return nil, nil // nothing to delete on a "new session" row
		}
		for i := range items {
			if items[i].SessionID == meta {
				return &PickerResult{DeleteRequest: true, Item: &items[i]}, nil
			}
		}
		return nil, fmt.Errorf("session %q not found in items", meta)
	}

	if strings.HasPrefix(meta, "new:") {
		wsName := strings.TrimPrefix(meta, "new:")
		for i := range items {
			if items[i].IsNew && items[i].WorkspaceName == wsName {
				return &PickerResult{Item: &items[i]}, nil
			}
		}
		return nil, fmt.Errorf("workspace %q not found in items", wsName)
	}

	for i := range items {
		if items[i].SessionID == meta {
			return &PickerResult{Item: &items[i]}, nil
		}
	}
	return nil, fmt.Errorf("session %q not found in items", meta)
}

func FilterItemsByWorkspace(items []PickerItem, workspaceName string) []PickerItem {
	var filtered []PickerItem
	for _, item := range items {
		if item.WorkspaceName == workspaceName {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func RunWorkspacePicker(workspaces []db.Workspace, includeAll bool) (*db.Workspace, error) {
	width := getTerminalWidth()

	var lines []string
	if includeAll {
		label := ansiDimBright + "All workspaces" + ansiReset
		lines = append(lines, label+"\t"+AllWorkspacesName)
	}
	for _, ws := range workspaces {
		wsColour := colourForWorkspace(ws.Name)
		name := wsColour + ws.Name + ansiReset
		padding := width - len(ws.Name) - 2
		if padding < 2 {
			padding = 2
		}
		pathDisplay := ansiDim + ws.Path + ansiReset
		displayed := name + strings.Repeat(" ", padding) + pathDisplay
		lines = append(lines, displayed+"\t"+ws.Path)
	}

	input := strings.Join(lines, "\n")

	wsHeader := "  Select workspace for new session"
	if includeAll {
		wsHeader = "  Filter by workspace"
	}

	cmd := exec.Command("fzf",
		"--ansi",
		"--no-multi",
		"--delimiter=\t",
		"--with-nth=1",
		"--layout=reverse",
		"--no-separator",
		"--header="+wsHeader,
		"--prompt=  ",
		"--pointer=▸",
		"--info=hidden",
		"--color=fg:#e6edf3,fg+:#f0f6fc:bold,bg:#0d1117,bg+:#161b22,hl:#3fb950,hl+:#3fb950:bold,info:#8b949e,prompt:#58a6ff,pointer:#58a6ff,header:#8b949e,gutter:#0d1117,border:#30363d",
		"--preview-window=hidden",
	)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code == 1 || code == 130 {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("running workspace picker: %w", err)
	}

	selected := strings.TrimSpace(string(out))
	if selected == "" {
		return nil, nil
	}

	parts := strings.SplitN(selected, "\t", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected fzf output format")
	}
	path := parts[1]
	if path == AllWorkspacesName {
		return &db.Workspace{Name: AllWorkspacesName}, nil
	}
	for i := range workspaces {
		if workspaces[i].Path == path {
			return &workspaces[i], nil
		}
	}
	return nil, fmt.Errorf("workspace with path %q not found", path)
}

func shortName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func colourForWorkspace(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return workspaceColours[int(h.Sum32())%len(workspaceColours)]
}

func statusIndicator(status string) string {
	switch status {
	case "busy":
		return ansiYellow + "● " + ansiReset
	case "retry":
		return ansiRed + "↻ " + ansiReset
	default:
		return "  "
	}
}

func colouredAge(t time.Time) string {
	if t.IsZero() {
		return "    "
	}
	d := time.Since(t)
	var text, colour string
	switch {
	case d < time.Minute:
		text = "now"
		colour = ansiGreen
	case d < time.Hour:
		text = fmt.Sprintf("%dm", int(d.Minutes()))
		colour = ansiYellow
	case d < 24*time.Hour:
		text = fmt.Sprintf("%dh", int(d.Hours()))
	default:
		text = fmt.Sprintf("%dd", int(d.Hours()/24))
		colour = ansiDim
	}
	padded := fmt.Sprintf("%4s", text)
	if colour != "" {
		return colour + padded + ansiReset
	}
	return padded
}

func getTerminalWidth() int {
	for _, f := range []*os.File{os.Stderr, os.Stdout, os.Stdin} {
		w, _, err := term.GetSize(int(f.Fd()))
		if err == nil && w > 0 {
			return w
		}
	}
	return 80
}

func truncate(s string, maxLen int) string {
	if maxLen < 4 {
		maxLen = 4
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

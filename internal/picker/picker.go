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
)

var workspaceColors = []string{
	"\033[38;5;114m", // green
	"\033[38;5;180m", // gold
	"\033[38;5;139m", // mauve
	"\033[38;5;74m",  // blue
	"\033[38;5;173m", // salmon
	"\033[38;5;109m", // teal
	"\033[38;5;176m", // pink
	"\033[38;5;149m", // lime
	"\033[38;5;146m", // lavender
	"\033[38;5;216m", // peach
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

func FormatPickerLine(item PickerItem, width int) string {
	width -= 4 // account for fzf pointer and chrome
	if width < 40 {
		width = 40
	}

	wsColor := colorForWorkspace(item.WorkspaceName)
	wsName := item.WorkspaceName

	if item.IsNew {
		ageField := "   +"
		title := "Create new session"
		leftW := 2 + 4 + 2 + len(title) // indicator + age + gap + title
		rightW := len(wsName)
		padding := width - leftW - rightW
		if padding < 2 {
			padding = 2
		}
		return ansiDimBright + "  " + ageField + "  " + title +
			strings.Repeat(" ", padding) + wsColor + wsName + ansiReset
	}

	indicator := statusIndicator(item.Status)
	age := coloredAge(item.UpdatedAt)

	maxTitle := width - 2 - 4 - 2 - 2 - len(wsName)
	if maxTitle < 10 {
		maxTitle = 10
	}
	title := truncate(item.SessionTitle, maxTitle)

	leftW := 2 + 4 + 2 + len(title) // indicator + age + gap + title (visible chars)
	rightW := len(wsName)
	padding := width - leftW - rightW
	if padding < 2 {
		padding = 2
	}

	return indicator + age + "  " + title +
		strings.Repeat(" ", padding) + wsColor + wsName + ansiReset
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
		sessions := sessionsByDir[ws.Path]

		if len(sessions) == 0 {
			items = append(items, PickerItem{
				WorkspaceName: ws.Name,
				WorkspacePath: ws.Path,
				IsNew:         true,
			})
			continue
		}

		for _, s := range sessions {
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

		items = append(items, PickerItem{
			WorkspaceName: ws.Name,
			WorkspacePath: ws.Path,
			IsNew:         true,
		})
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
	Item       *PickerItem
	NewRequest bool
}

func RunFzf(items []PickerItem) (*PickerResult, error) {
	width := getTerminalWidth()

	var lines []string
	for _, item := range items {
		displayed := FormatPickerLine(item, width)
		meta := item.SessionID
		if item.IsNew {
			meta = "new:" + item.WorkspaceName
		}
		lines = append(lines, displayed+"\t"+meta)
	}

	input := strings.Join(lines, "\n")

	cmd := exec.Command("fzf",
		"--ansi",
		"--no-multi",
		"--delimiter=\t",
		"--with-nth=1",
		"--layout=reverse",
		"--no-separator",
		"--header=  ctrl-n: new session  ·  ctrl-d: delete  ·  ctrl-c: cancel",
		"--prompt=  ",
		"--pointer=▸",
		"--color=fg:-1,fg+:white:bold,bg:-1,bg+:-1,hl:yellow,hl+:yellow:bold,info:grey,prompt:blue,pointer:blue,header:gray",
		"--preview-window=hidden",
		"--bind=ctrl-d:execute-silent(echo delete:{2})+abort",
		"--expect=ctrl-n",
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

	if len(outputLines) < 2 || outputLines[1] == "" {
		return nil, nil
	}

	selected := outputLines[1]
	parts := strings.SplitN(selected, "\t", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected fzf output format")
	}

	meta := parts[1]
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

func RunWorkspacePicker(workspaces []db.Workspace) (*db.Workspace, error) {
	width := getTerminalWidth()

	var lines []string
	for _, ws := range workspaces {
		wsColor := colorForWorkspace(ws.Name)
		name := wsColor + ws.Name + ansiReset
		padding := width - len(ws.Name) - 2
		if padding < 2 {
			padding = 2
		}
		pathDisplay := ansiDim + ws.Path + ansiReset
		displayed := name + strings.Repeat(" ", padding) + pathDisplay
		lines = append(lines, displayed+"\t"+ws.Path)
	}

	input := strings.Join(lines, "\n")

	cmd := exec.Command("fzf",
		"--ansi",
		"--no-multi",
		"--delimiter=\t",
		"--with-nth=1",
		"--layout=reverse",
		"--no-separator",
		"--header=  Select workspace for new session",
		"--prompt=  ",
		"--pointer=▸",
		"--color=fg:-1,fg+:white:bold,bg:-1,bg+:-1,hl:yellow,hl+:yellow:bold,info:grey,prompt:blue,pointer:blue,header:gray",
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
	for i := range workspaces {
		if workspaces[i].Path == path {
			return &workspaces[i], nil
		}
	}
	return nil, fmt.Errorf("workspace with path %q not found", path)
}

func colorForWorkspace(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return workspaceColors[int(h.Sum32())%len(workspaceColors)]
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

func coloredAge(t time.Time) string {
	if t.IsZero() {
		return "    "
	}
	d := time.Since(t)
	var text, color string
	switch {
	case d < time.Minute:
		text = "now"
		color = ansiGreen
	case d < time.Hour:
		text = fmt.Sprintf("%dm", int(d.Minutes()))
		color = ansiYellow
	case d < 24*time.Hour:
		text = fmt.Sprintf("%dh", int(d.Hours()))
	default:
		text = fmt.Sprintf("%dd", int(d.Hours()/24))
		color = ansiDim
	}
	padded := fmt.Sprintf("%4s", text)
	if color != "" {
		return color + padded + ansiReset
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

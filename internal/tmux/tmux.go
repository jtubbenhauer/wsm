package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var debugLogPath string

func init() {
	stateDir := os.Getenv("XDG_STATE_HOME")
	if stateDir == "" {
		home, _ := os.UserHomeDir()
		stateDir = filepath.Join(home, ".local", "state")
	}
	debugLogPath = filepath.Join(stateDir, "wsm", "debug.log")
}

func logDebug(format string, args ...interface{}) {
	os.MkdirAll(filepath.Dir(debugLogPath), 0o755)
	f, err := os.OpenFile(debugLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(f, "[%s] %s\n", ts, fmt.Sprintf(format, args...))
}

// paneListFor returns a debug-friendly summary of panes in a window.
func paneListFor(sessionName string, windowIndex int) string {
	target := fmt.Sprintf("=%s:%d", sessionName, windowIndex)
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_index}:#{pane_id}:#{pane_current_command}").Output()
	if err != nil {
		return fmt.Sprintf("<err: %v>", err)
	}
	return strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " | ")
}

func HasSession(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", "="+name)
	return cmd.Run() == nil
}

func SwitchClient(name string) error {
	cmd := exec.Command("tmux", "switch-client", "-t", "="+name)
	return cmd.Run()
}

func AttachSession(name string) error {
	cmd := exec.Command("tmux", "attach-session", "-t", "="+name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func KillPane(sessionName string, windowIndex, paneIndex int) error {
	target := fmt.Sprintf("=%s:%d.%d", sessionName, windowIndex, paneIndex)
	cmd := exec.Command("tmux", "kill-pane", "-t", target)
	return cmd.Run()
}

func SendKeys(sessionName string, windowIndex, paneIndex int, keys string) error {
	target := fmt.Sprintf("=%s:%d.%d", sessionName, windowIndex, paneIndex)
	cmd := exec.Command("tmux", "send-keys", "-t", target, keys, "Enter")
	return cmd.Run()
}

func SelectWindow(sessionName string, windowIndex int) error {
	target := fmt.Sprintf("=%s:%d", sessionName, windowIndex)
	cmd := exec.Command("tmux", "select-window", "-t", target)
	return cmd.Run()
}

func IsInsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

func SanitiseName(name string) string {
	replacer := strings.NewReplacer(".", "_", ":", "_", " ", "_", "/", "_")
	return replacer.Replace(name)
}

func SetEnvironment(sessionName, key, value string) error {
	cmd := exec.Command("tmux", "set-environment", "-t", "="+sessionName, key, value)
	return cmd.Run()
}

func GetEnvironment(sessionName, key string) string {
	cmd := exec.Command("tmux", "show-environment", "-t", "="+sessionName, key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Output format: KEY=VALUE\n
	line := strings.TrimSpace(string(out))
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

type SessionLayout struct {
	Name            string
	WorkspacePath   string
	SessionID       string
	SessionFilePath string // full path to JSONL file, empty for new sessions
}

type PaneState int

const (
	PaneAlive PaneState = iota
	PaneDead
	PaneGone
)

func paneEnvKey(sessionID string) string {
	return "WSM_PANE_" + sessionID
}

func CheckPane(paneID string) PaneState {
	cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_id}\t#{pane_dead}")
	out, err := cmd.Output()
	if err != nil {
		return PaneGone
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)
	if len(parts) != 2 || parts[0] != paneID {
		return PaneGone
	}
	if parts[1] == "1" {
		return PaneDead
	}
	return PaneAlive
}

func SwapPanes(paneA, paneB string) error {
	cmd := exec.Command("tmux", "swap-pane", "-s", paneA, "-t", paneB)
	return cmd.Run()
}

func CreateParkedPane(session string, layout SessionLayout) (string, error) {
	cmd := piCommand(layout)
	suffix := layout.SessionID
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	windowName := "_park_" + suffix
	tmuxCmd := exec.Command("tmux", "new-window", "-t", "="+session+":", "-n", windowName, "-d",
		"-c", layout.WorkspacePath,
		"-P", "-F", "#{pane_id}",
		"zsh", "-c", cmd+"; exec zsh")
	out, err := tmuxCmd.Output()
	if err != nil {
		return "", fmt.Errorf("creating parked pane: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func CleanupParkedPane(sessionName, sessionID string) {
	name := SanitiseName(sessionName)
	paneID := GetEnvironment(name, paneEnvKey(sessionID))
	if paneID != "" {
		exec.Command("tmux", "kill-pane", "-t", paneID).Run()
	}
	exec.Command("tmux", "set-environment", "-u", "-t", "="+name, paneEnvKey(sessionID)).Run()
}

func RespawnPane(paneID string, layout SessionLayout) error {
	cmd := piCommand(layout)
	tmuxCmd := exec.Command("tmux", "respawn-pane", "-t", paneID, "zsh", "-c", cmd+"; exec zsh")
	return tmuxCmd.Run()
}

func discoverPiPane(session string) string {
	target := "=" + session + ":pi"
	cmd := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_id}\t#{pane_current_command}")
	out, err := cmd.Output()
	if err != nil {
		logDebug("  discoverPiPane: list-panes failed target=%s err=%v", target, err)
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		paneCmd := parts[1]
		if paneCmd == "zsh" || paneCmd == "bash" || paneCmd == "fish" {
			continue
		}
		logDebug("  discoverPiPane: picked %s (cmd=%s)", parts[0], paneCmd)
		return parts[0]
	}
	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(lines) > 0 {
		parts := strings.SplitN(lines[0], "\t", 2)
		if len(parts) >= 1 {
			logDebug("  discoverPiPane: fallback to first pane %s", parts[0])
			return parts[0]
		}
	}
	logDebug("  discoverPiPane: no panes found")
	return ""
}

func piCommand(layout SessionLayout) string {
	if layout.SessionFilePath != "" {
		return fmt.Sprintf("pi --session %s", layout.SessionFilePath)
	}
	return "pi"
}

// CreateWorkspaceSession creates a 3-window tmux session:
//
//	Window 1: nvim . (focused on create)
//	Window 2: pi attach (left) | empty shell (right)
//	Window 3: lazygit
func CreateWorkspaceSession(layout SessionLayout) error {
	name := SanitiseName(layout.Name)
	logDebug("CreateWorkspaceSession: name=%s sessionID=%s path=%s", name, layout.SessionID, layout.WorkspacePath)

	if HasSession(name) {
		logDebug("  session exists, switching")
		return switchToPiSession(name, layout)
	}

	cmd := piCommand(layout)

	// Window 1: nvim (created with the session itself via direct execution)
	nvimCmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-c", layout.WorkspacePath, "-n", "nvim", "nvim", ".")
	if err := nvimCmd.Run(); err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}
	logDebug("  created session+window1 (nvim)")

	// Window 2: pi attach via pi command (capture pane ID for parking)
	createPi := exec.Command("tmux", "new-window", "-t", "="+name, "-n", "pi", "-c", layout.WorkspacePath, "-P", "-F", "#{pane_id}", "zsh", "-c", cmd+"; exec zsh")
	piPaneOut, err := createPi.Output()
	if err != nil {
		return fmt.Errorf("creating pi window: %w", err)
	}
	piPaneID := strings.TrimSpace(string(piPaneOut))
	logDebug("  created window2 (pi) pane=%s", piPaneID)

	// Split right for shell
	splitCmd := exec.Command("tmux", "split-window", "-h", "-t", fmt.Sprintf("=%s:2", name), "-c", layout.WorkspacePath)
	splitOut, err := splitCmd.CombinedOutput()
	if err != nil {
		logDebug("  split-window FAILED: err=%v out=%q", err, string(splitOut))
		return fmt.Errorf("splitting pi window: %w (%s)", err, string(splitOut))
	}
	logDebug("  split-window ok, panes in window2: %s", paneListFor(name, 2))

	// Select the left pane (pi)
	selectPane := exec.Command("tmux", "select-pane", "-t", fmt.Sprintf("=%s:2.1", name))
	if err := selectPane.Run(); err != nil {
		return fmt.Errorf("selecting pi pane: %w", err)
	}
	logDebug("  selected pane 2.1, panes in window2: %s", paneListFor(name, 2))

	// Window 3: lazygit (direct execution)
	createLazygit := exec.Command("tmux", "new-window", "-t", "="+name, "-n", "lazygit", "-c", layout.WorkspacePath, "lazygit")
	if err := createLazygit.Run(); err != nil {
		return fmt.Errorf("creating lazygit window: %w", err)
	}

	// Set session env var for skip-reload logic
	if err := SetEnvironment(name, "WSM_SESSION_ID", layout.SessionID); err != nil {
		return fmt.Errorf("setting session env var: %w", err)
	}

	// Store pane ID mapping for parking lookups
	if err := SetEnvironment(name, paneEnvKey(layout.SessionID), piPaneID); err != nil {
		return fmt.Errorf("storing pane mapping: %w", err)
	}

	// Focus window 2 (pi)
	if err := SelectWindow(name, 2); err != nil {
		return fmt.Errorf("selecting pi window: %w", err)
	}
	logDebug("  final state, panes in window2: %s", paneListFor(name, 2))

	return nil
}

// switchToPiSession swaps the active pi pane with a parked pane
// for the target session, preserving both processes alive.
func switchToPiSession(name string, layout SessionLayout) error {
	logDebug("switchToPiSession: name=%s targetSession=%s", name, layout.SessionID)

	currentSessionID := GetEnvironment(name, "WSM_SESSION_ID")
	logDebug("  currentSessionID=%s", currentSessionID)

	if currentSessionID == layout.SessionID {
		logDebug("  same session, just selecting window 2")
		return SelectWindow(name, 2)
	}

	targetPaneID := GetEnvironment(name, paneEnvKey(layout.SessionID))
	logDebug("  targetPaneID from env: %q", targetPaneID)

	if targetPaneID != "" {
		targetState := CheckPane(targetPaneID)
		logDebug("  targetPane state: %d", targetState)
		switch targetState {
		case PaneGone:
			logDebug("  targetPane gone, will create new")
			targetPaneID = ""
		case PaneDead:
			logDebug("  targetPane dead, respawning")
			if err := RespawnPane(targetPaneID, layout); err != nil {
				return fmt.Errorf("respawning dead pane: %w", err)
			}
		}
	}

	if targetPaneID == "" {
		paneID, err := CreateParkedPane(name, layout)
		if err != nil {
			return fmt.Errorf("creating parked pane: %w", err)
		}
		targetPaneID = paneID
		logDebug("  created parked pane: %s", targetPaneID)
		if err := SetEnvironment(name, paneEnvKey(layout.SessionID), targetPaneID); err != nil {
			return fmt.Errorf("storing pane mapping: %w", err)
		}
	}

	activePaneID := GetEnvironment(name, paneEnvKey(currentSessionID))
	logDebug("  activePaneID from env: %q (key=%s)", activePaneID, paneEnvKey(currentSessionID))

	if activePaneID != "" {
		activeState := CheckPane(activePaneID)
		logDebug("  activePane state: %d", activeState)
		if activeState == PaneGone {
			activePaneID = ""
		}
	}
	if activePaneID == "" {
		activePaneID = discoverPiPane(name)
		logDebug("  discovered activePaneID: %q", activePaneID)
		if activePaneID == "" {
			return fmt.Errorf("cannot find pi pane in session %s", name)
		}
		if currentSessionID != "" {
			SetEnvironment(name, paneEnvKey(currentSessionID), activePaneID)
		}
	}

	logDebug("  swapping active=%s <-> target=%s", activePaneID, targetPaneID)
	if err := SwapPanes(activePaneID, targetPaneID); err != nil {
		return fmt.Errorf("swapping panes: %w", err)
	}

	// Focus the swapped-in pane so the correct content is visible
	selectCmd := exec.Command("tmux", "select-pane", "-t", targetPaneID)
	if err := selectCmd.Run(); err != nil {
		logDebug("  select-pane failed: %v", err)
	}

	if err := SetEnvironment(name, "WSM_SESSION_ID", layout.SessionID); err != nil {
		return fmt.Errorf("setting session env var: %w", err)
	}

	logDebug("  done, selecting window 2")
	return SelectWindow(name, 2)
}

func RunInTerminal(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// PromptViaNvim opens an nvim buffer for the user to type or edit a single-line
// value and returns the trimmed contents. The buffer opens in normal mode with
// the cursor on line 1; prefillValue (if non-empty) is placed on line 1 for
// editing. Lines beginning with '#' are treated as instructional comments and
// stripped. Empty result is valid (cancel/skip).
func PromptViaNvim(prompt, prefillValue string) (string, error) {
	logDebug("PromptViaNvim: start prompt=%q prefill=%q", prompt, prefillValue)
	tmpFile, err := os.CreateTemp("", "wsm-prompt-*.txt")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	prefill := fmt.Sprintf("%s\n# %s (edit above, :wq to confirm, empty = cancel)\n", prefillValue, prompt)
	if _, err := tmpFile.WriteString(prefill); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("writing prefill: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("closing temp file: %w", err)
	}

	nvimArgs := []string{
		"nvim",
		"-c", "set filetype=conf",
		"-c", "set nonumber norelativenumber",
		tmpPath,
	}

	if err := RunInTerminal(nvimArgs...); err != nil {
		return "", fmt.Errorf("running nvim: %w", err)
	}
	logDebug("PromptViaNvim: nvim exited")

	contents, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("reading prompt file: %w", err)
	}

	var meaningful []string
	for _, line := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		meaningful = append(meaningful, trimmed)
	}
	result := strings.TrimSpace(strings.Join(meaningful, " "))
	logDebug("PromptViaNvim: result=%q", result)
	return result, nil
}

func DisplayPopup(workingDir string, args ...string) error {
	cmdArgs := []string{"display-popup", "-E", "-w", "80%", "-h", "80%", "-d", workingDir}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("tmux", cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func KillSession(name string) error {
	sanitised := SanitiseName(name)
	cmd := exec.Command("tmux", "kill-session", "-t", "="+sanitised)
	return cmd.Run()
}

func ListSessions() []string {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var sessions []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions
}

func SwitchOrAttach(name string) error {
	sanitised := SanitiseName(name)
	if IsInsideTmux() {
		return SwitchClient(sanitised)
	}
	return AttachSession(sanitised)
}

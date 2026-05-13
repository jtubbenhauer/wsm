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

func piCommand(layout SessionLayout) string {
	if layout.SessionFilePath != "" {
		return fmt.Sprintf("pi --session %s", layout.SessionFilePath)
	}
	return "pi"
}

// CreateWorkspaceSession creates a 3-window tmux session with shell-persistent panes:
//   Window 1: nvim (exits -> shell remains)
//   Window 2: pi (left, in shell) | shell (right)
//   Window 3: lazygit (exits -> shell remains)
func CreateWorkspaceSession(layout SessionLayout) error {
	name := SanitiseName(layout.Name)
	logDebug("CreateWorkspaceSession: name=%s sessionID=%s path=%s", name, layout.SessionID, layout.WorkspacePath)

	if HasSession(name) {
		logDebug("  session exists, sending pi command")
		return SendToPiPane(name, layout)
	}

	piCmd := piCommand(layout)

	// Window 1: nvim in a persistent shell
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-c", layout.WorkspacePath, "-n", "nvim",
		"zsh", "-c", "nvim .; exec zsh")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}
	logDebug("  created session+window1 (nvim)")

	// Window 2: pi in a persistent shell (left pane)
	createPi := exec.Command("tmux", "new-window", "-t", "="+name, "-n", "pi", "-c", layout.WorkspacePath,
		"zsh", "-c", piCmd+"; exec zsh")
	if err := createPi.Run(); err != nil {
		return fmt.Errorf("creating pi window: %w", err)
	}
	logDebug("  created window2 (pi)")

	// Split right for shell
	splitCmd := exec.Command("tmux", "split-window", "-h", "-t", fmt.Sprintf("=%s:2", name), "-c", layout.WorkspacePath)
	if err := splitCmd.Run(); err != nil {
		return fmt.Errorf("splitting pi window: %w", err)
	}
	logDebug("  split-window ok, panes in window2: %s", paneListFor(name, 2))

	// Select the left pane (pi)
	selectPane := exec.Command("tmux", "select-pane", "-t", fmt.Sprintf("=%s:2.1", name))
	if err := selectPane.Run(); err != nil {
		return fmt.Errorf("selecting pi pane: %w", err)
	}

	// Window 3: lazygit in a persistent shell
	createLazygit := exec.Command("tmux", "new-window", "-t", "="+name, "-n", "lazygit", "-c", layout.WorkspacePath,
		"zsh", "-c", "lazygit; exec zsh")
	if err := createLazygit.Run(); err != nil {
		return fmt.Errorf("creating lazygit window: %w", err)
	}

	if layout.SessionID != "" {
		if err := SetEnvironment(name, "WSM_SESSION_ID", layout.SessionID); err != nil {
			return fmt.Errorf("setting session env var: %w", err)
		}
	}

	if err := SelectWindow(name, 2); err != nil {
		return fmt.Errorf("selecting pi window: %w", err)
	}
	logDebug("  final state, panes in window2: %s", paneListFor(name, 2))

	return nil
}

// SendToPiPane exits any running Pi and relaunches with the target session.
func SendToPiPane(sessionName string, layout SessionLayout) error {
	name := SanitiseName(sessionName)
	logDebug("SendToPiPane: name=%s sessionID=%s filePath=%s", name, layout.SessionID, layout.SessionFilePath)

	currentSessionID := GetEnvironment(name, "WSM_SESSION_ID")
	if currentSessionID == layout.SessionID && layout.SessionID != "" {
		logDebug("  same session, just selecting window 2")
		return SelectWindow(name, 2)
	}

	target := fmt.Sprintf("=%s:2.1", name)

	// Ctrl-C interrupts current operation, Ctrl-D exits Pi (harmless if already at shell)
	exec.Command("tmux", "send-keys", "-t", target, "C-c").Run()
	time.Sleep(100 * time.Millisecond)
	exec.Command("tmux", "send-keys", "-t", target, "C-d").Run()
	time.Sleep(500 * time.Millisecond)

	cmd := piCommand(layout)
	logDebug("  sending cmd=%q", cmd)
	send := exec.Command("tmux", "send-keys", "-t", target, cmd, "Enter")
	if err := send.Run(); err != nil {
		return fmt.Errorf("sending pi command to pane: %w", err)
	}

	if layout.SessionID != "" {
		if err := SetEnvironment(name, "WSM_SESSION_ID", layout.SessionID); err != nil {
			return fmt.Errorf("setting session env var: %w", err)
		}
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

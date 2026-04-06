package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func HasSession(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

func SwitchClient(name string) error {
	cmd := exec.Command("tmux", "switch-client", "-t", name)
	return cmd.Run()
}

func AttachSession(name string) error {
	cmd := exec.Command("tmux", "attach-session", "-t", name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func KillPane(sessionName string, windowIndex, paneIndex int) error {
	target := fmt.Sprintf("%s:%d.%d", sessionName, windowIndex, paneIndex)
	cmd := exec.Command("tmux", "kill-pane", "-t", target)
	return cmd.Run()
}

func SendKeys(sessionName string, windowIndex, paneIndex int, keys string) error {
	target := fmt.Sprintf("%s:%d.%d", sessionName, windowIndex, paneIndex)
	cmd := exec.Command("tmux", "send-keys", "-t", target, keys, "Enter")
	return cmd.Run()
}

func SelectWindow(sessionName string, windowIndex int) error {
	target := fmt.Sprintf("%s:%d", sessionName, windowIndex)
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
	cmd := exec.Command("tmux", "set-environment", "-t", sessionName, key, value)
	return cmd.Run()
}

func GetEnvironment(sessionName, key string) string {
	cmd := exec.Command("tmux", "show-environment", "-t", sessionName, key)
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
	Name          string
	WorkspacePath string
	SessionID     string
}

// CreateWorkspaceSession creates a 3-window tmux session:
//
//	Window 1: nvim . (focused on create)
//	Window 2: opencode attach (left) | empty shell (right)
//	Window 3: lazygit
func CreateWorkspaceSession(layout SessionLayout) error {
	name := SanitiseName(layout.Name)

	if HasSession(name) {
		return switchToOpenCodeSession(name, layout)
	}

	attachCmd := openCodeAttachCommand(layout.SessionID, layout.WorkspacePath)

	// Window 1: nvim (created with the session itself via direct execution)
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-c", layout.WorkspacePath, "-n", "nvim", "nvim", ".")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}

	// Window 2: opencode attach via wsm attach (direct execution)
	createOC := exec.Command("tmux", "new-window", "-t", name, "-n", "opencode", "-c", layout.WorkspacePath, "sh", "-c", attachCmd)
	if err := createOC.Run(); err != nil {
		return fmt.Errorf("creating opencode window: %w", err)
	}

	// Split right for shell
	splitCmd := exec.Command("tmux", "split-window", "-h", "-t", fmt.Sprintf("%s:2", name), "-c", layout.WorkspacePath)
	if err := splitCmd.Run(); err != nil {
		return fmt.Errorf("splitting opencode window: %w", err)
	}

	// Select the left pane (opencode)
	selectPane := exec.Command("tmux", "select-pane", "-t", fmt.Sprintf("%s:2.1", name))
	if err := selectPane.Run(); err != nil {
		return fmt.Errorf("selecting opencode pane: %w", err)
	}

	// Window 3: lazygit (direct execution)
	createLazygit := exec.Command("tmux", "new-window", "-t", name, "-n", "lazygit", "-c", layout.WorkspacePath, "lazygit")
	if err := createLazygit.Run(); err != nil {
		return fmt.Errorf("creating lazygit window: %w", err)
	}

	// Set session env var for skip-reload logic
	if err := SetEnvironment(name, "WSM_SESSION_ID", layout.SessionID); err != nil {
		return fmt.Errorf("setting session env var: %w", err)
	}

	// Focus window 2 (opencode)
	if err := SelectWindow(name, 2); err != nil {
		return fmt.Errorf("selecting opencode window: %w", err)
	}

	return nil
}

// switchToOpenCodeSession replaces the opencode pane command for an existing session
func switchToOpenCodeSession(name string, layout SessionLayout) error {
	currentSessionID := GetEnvironment(name, "WSM_SESSION_ID")
	if currentSessionID == layout.SessionID {
		return SelectWindow(name, 2)
	}

	target := fmt.Sprintf("%s:2.1", name)
	attachCmd := openCodeAttachCommand(layout.SessionID, layout.WorkspacePath)
	respawn := exec.Command("tmux", "respawn-pane", "-k", "-t", target, "sh", "-c", attachCmd)
	if err := respawn.Run(); err != nil {
		return fmt.Errorf("respawning opencode pane: %w", err)
	}

	if err := SetEnvironment(name, "WSM_SESSION_ID", layout.SessionID); err != nil {
		return fmt.Errorf("setting session env var: %w", err)
	}

	return SelectWindow(name, 2)
}

func openCodeAttachCommand(sessionID, directory string) string {
	return fmt.Sprintf("wsm attach -s %s --dir %s", sessionID, directory)
}

func SwitchOrAttach(name string) error {
	sanitised := SanitiseName(name)
	if IsInsideTmux() {
		return SwitchClient(sanitised)
	}
	return AttachSession(sanitised)
}

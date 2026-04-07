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

	// Window 2: opencode attach via wsm attach (capture pane ID for parking)
	createOC := exec.Command("tmux", "new-window", "-t", name, "-n", "opencode", "-c", layout.WorkspacePath, "-P", "-F", "#{pane_id}", "sh", "-c", attachCmd)
	ocPaneOut, err := createOC.Output()
	if err != nil {
		return fmt.Errorf("creating opencode window: %w", err)
	}
	ocPaneID := strings.TrimSpace(string(ocPaneOut))

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

	// Store pane ID mapping for parking lookups
	if err := SetEnvironment(name, paneEnvKey(layout.SessionID), ocPaneID); err != nil {
		return fmt.Errorf("storing pane mapping: %w", err)
	}

	// Focus window 2 (opencode)
	if err := SelectWindow(name, 2); err != nil {
		return fmt.Errorf("selecting opencode window: %w", err)
	}

	return nil
}

// switchToOpenCodeSession swaps the active opencode pane with a parked pane
// for the target session, preserving both processes alive.
func switchToOpenCodeSession(name string, layout SessionLayout) error {
	currentSessionID := GetEnvironment(name, "WSM_SESSION_ID")
	if currentSessionID == layout.SessionID {
		return SelectWindow(name, 2)
	}

	targetPaneID := GetEnvironment(name, paneEnvKey(layout.SessionID))

	if targetPaneID != "" {
		switch CheckPane(targetPaneID) {
		case PaneGone:
			targetPaneID = ""
		case PaneDead:
			if err := RespawnPane(targetPaneID, layout); err != nil {
				return fmt.Errorf("respawning dead pane: %w", err)
			}
		}
	}

	if targetPaneID == "" {
		if err := EnsureParkingWindow(name); err != nil {
			return fmt.Errorf("ensuring parking window: %w", err)
		}
		paneID, err := CreateParkedPane(name, layout)
		if err != nil {
			return fmt.Errorf("creating parked pane: %w", err)
		}
		targetPaneID = paneID
		if err := SetEnvironment(name, paneEnvKey(layout.SessionID), targetPaneID); err != nil {
			return fmt.Errorf("storing pane mapping: %w", err)
		}
	}

	activePaneID := GetEnvironment(name, paneEnvKey(currentSessionID))
	if activePaneID == "" {
		return fmt.Errorf("no tracked pane for active session %s", currentSessionID)
	}

	if err := SwapPanes(activePaneID, targetPaneID); err != nil {
		return fmt.Errorf("swapping panes: %w", err)
	}

	if err := SetEnvironment(name, "WSM_SESSION_ID", layout.SessionID); err != nil {
		return fmt.Errorf("setting session env var: %w", err)
	}

	return SelectWindow(name, 2)
}

func openCodeAttachCommand(sessionID, directory string) string {
	return fmt.Sprintf("wsm attach -s %s --dir %s", sessionID, directory)
}

const parkingWindow = "_parking"

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
	cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_dead}")
	out, err := cmd.Output()
	if err != nil {
		return PaneGone
	}
	if strings.TrimSpace(string(out)) == "1" {
		return PaneDead
	}
	return PaneAlive
}

func SwapPanes(paneA, paneB string) error {
	cmd := exec.Command("tmux", "swap-pane", "-s", paneA, "-t", paneB)
	return cmd.Run()
}

func HasWindow(session, windowName string) bool {
	target := fmt.Sprintf("%s:%s", session, windowName)
	cmd := exec.Command("tmux", "list-panes", "-t", target)
	return cmd.Run() == nil
}

func EnsureParkingWindow(session string) error {
	if HasWindow(session, parkingWindow) {
		return nil
	}
	cmd := exec.Command("tmux", "new-window", "-t", session+":", "-n", parkingWindow, "-d")
	return cmd.Run()
}

func CreateParkedPane(session string, layout SessionLayout) (string, error) {
	attachCmd := openCodeAttachCommand(layout.SessionID, layout.WorkspacePath)
	target := fmt.Sprintf("%s:%s", session, parkingWindow)
	cmd := exec.Command("tmux", "split-window", "-t", target, "-d",
		"-c", layout.WorkspacePath,
		"-P", "-F", "#{pane_id}",
		"sh", "-c", attachCmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("creating parked pane: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func RespawnPane(paneID string, layout SessionLayout) error {
	attachCmd := openCodeAttachCommand(layout.SessionID, layout.WorkspacePath)
	cmd := exec.Command("tmux", "respawn-pane", "-t", paneID, "sh", "-c", attachCmd)
	return cmd.Run()
}

func RunInTerminal(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

func SwitchOrAttach(name string) error {
	sanitised := SanitiseName(name)
	if IsInsideTmux() {
		return SwitchClient(sanitised)
	}
	return AttachSession(sanitised)
}

# wsm

Workspace Session Manager. A tmux-based picker for managing [opencode](https://opencode.ai/) sessions across multiple workspaces.

wsm registers git repositories as workspaces, then provides a fuzzy picker (via fzf) to create and switch between opencode sessions. Selecting a session opens a tmux session with three windows: nvim, opencode + a shell pane, and lazygit.

## Prerequisites

| Tool | Purpose |
|---|---|
| [tmux](https://github.com/tmux/tmux) | Terminal multiplexer |
| [opencode](https://opencode.ai/) | AI coding assistant |
| [fzf](https://github.com/junegunn/fzf) | Fuzzy finder for the interactive picker |
| [git](https://git-scm.com/) | Repository detection and worktree management |
| [nvim](https://neovim.io/) | Editor, launched in tmux window 1 |
| [lazygit](https://github.com/jesseduffield/lazygit) | Git TUI, launched in tmux window 3 |
| [Go](https://go.dev/) 1.26+ | Required to build from source |

## Installation

```bash
git clone https://github.com/jacksteamdev/wsm.git
cd wsm
./install.sh
```

This builds the binary and copies it to `~/.local/bin/wsm`. Make sure `~/.local/bin` is in your `PATH`.

### Recommended tmux keybindings

Add to your `~/.tmux.conf`:

```tmux
# prefix + s: Full session manager (all opencode sessions across workspaces)
bind-key s run-shell "tmux popup -E -w 80% -h 80% wsm"

# prefix + w: Quick workspace switcher (active tmux sessions only)
bind-key w display-popup -E -w 60% -h 40% "wsm switch"
```

## Quick Start

```bash
# 1. Discover git repos under ~/dev (default) and register them as workspaces
wsm scan

# 2. Open the picker, shows all opencode sessions across all workspaces
wsm

# 3. Select a session to switch to it, or create a new one
```

## Commands

| Command | Description |
|---|---|
| `wsm` | Open the interactive session picker |
| `wsm scan [dir]` | Auto-discover and register git repos (default: `~/dev`) |
| `wsm add <name> <path>` | Manually register a workspace |
| `wsm remove <name>` | Deregister a workspace (alias: `rm`) |
| `wsm list` | List all registered workspaces (alias: `ls`, supports `--json`) |
| `wsm info <name>` | Show workspace details and its opencode sessions |
| `wsm workspace <parent> <branch>` | Create a git worktree and register it as a workspace |
| `wsm switch` | Quick-switch between active workspace tmux sessions |
| `wsm plan [--dir] [--name]` | Open plan files from `.opencode/plans/` in nvim |

## Picker Controls

| Key | Action |
|---|---|
| `Enter` | Switch to the selected session |
| `ctrl-n` | Create a new session (opens a workspace sub-picker) |
| `ctrl-d` | Delete the selected session |
| `ctrl-r` | Rename the selected session |
| `ctrl-w` | Filter by workspace |
| `ctrl-x` | Kill a tmux workspace session |
| `ctrl-o` | Open plan files for the selected workspace in nvim |

### Status indicators

- Yellow dot: session is busy (opencode is actively working)
- Red arrow: session needs a retry

Sessions are sorted by most recently focused.

## Git Worktrees

`wsm workspace` creates a git worktree from a registered parent workspace and registers it as a new workspace. Worktrees are placed in `<parent-dir>/<parent-name>-worktrees/<branch>/`.

Symlinks from the parent workspace can be created with `--symlinks`:

```bash
wsm workspace my-project feature-branch --symlinks node_modules,.env
```

Use `--base` to specify the ref to branch from (defaults to `HEAD`).

## Plan Viewer

wsm can open plan files from a workspace's `.opencode/plans/` directory in nvim via a tmux floating popup.

**From the picker:** Press `ctrl-o` on any session to open its workspace's plan files. If multiple plan files exist, a secondary picker lets you choose.

**From a tmux session:** Run `wsm plan` from within a workspace directory. Add a tmux binding for quick access:

```tmux
bind-key o run-shell "tmux popup -E -w 80% -h 80% wsm plan"
```

Smart detection tries to match the workspace name against plan filenames (e.g., workspace `wsm` matches `wsm-plan.md`), falling back to the most recently modified file.

## Configuration

| Setting | Location / Default |
|---|---|
| Database | `$XDG_DATA_HOME/wsm/wsm.db` (default: `~/.local/share/wsm/wsm.db`) |
| OpenCode server | `127.0.0.1:4096` |
| OpenCode binary | `~/.opencode/bin/opencode`, falls back to `opencode` in `PATH` |
| Default scan directory | `~/dev` |

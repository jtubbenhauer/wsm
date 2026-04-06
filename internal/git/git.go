package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func IsGitRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func CurrentBranch(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting current branch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func RepoName(path string) string {
	return filepath.Base(path)
}

func IsWorktree(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-dir")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// Worktrees have git-dir pointing to parent's .git/worktrees/<name>
	return strings.Contains(strings.TrimSpace(string(out)), "/worktrees/")
}

func WorktreeParentPath(wtPath string) (string, error) {
	cmd := exec.Command("git", "-C", wtPath, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting common git dir: %w", err)
	}
	// common-dir is the parent's .git dir; resolve to repo root
	commonDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(wtPath, commonDir)
	}
	return filepath.Dir(filepath.Clean(commonDir)), nil
}

type ScanResult struct {
	Name       string
	Path       string
	IsWorktree bool
	ParentPath string
	Branch     string
}

func ScanForRepos(rootDir string) ([]ScanResult, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, fmt.Errorf("reading directory %s: %w", rootDir, err)
	}

	var results []ScanResult
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		fullPath := filepath.Join(rootDir, entry.Name())

		if IsGitRepo(fullPath) {
			result := ScanResult{Name: entry.Name(), Path: fullPath}
			if IsWorktree(fullPath) {
				result.IsWorktree = true
				result.ParentPath, _ = WorktreeParentPath(fullPath)
				result.Branch, _ = CurrentBranch(fullPath)
			}
			results = append(results, result)
			continue
		}

		// Recurse into *-worktrees/ directories
		if strings.HasSuffix(entry.Name(), "-worktrees") {
			nested, err := scanWorktreeDir(fullPath)
			if err != nil {
				continue
			}
			results = append(results, nested...)
		}
	}
	return results, nil
}

func scanWorktreeDir(dir string) ([]ScanResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	parentDirName := filepath.Base(dir)
	parentName := strings.TrimSuffix(parentDirName, "-worktrees")

	var results []ScanResult
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		if !IsGitRepo(fullPath) {
			continue
		}

		branch, _ := CurrentBranch(fullPath)
		parentPath, _ := WorktreeParentPath(fullPath)

		results = append(results, ScanResult{
			Name:       parentName + "/" + entry.Name(),
			Path:       fullPath,
			IsWorktree: true,
			ParentPath: parentPath,
			Branch:     branch,
		})
	}
	return results, nil
}

func SanitiseBranchName(branch string) string {
	replacer := strings.NewReplacer("/", "-", " ", "-", ":", "-")
	return replacer.Replace(branch)
}

func WorktreePath(parentPath, branch string) string {
	parentDir := filepath.Dir(parentPath)
	parentName := filepath.Base(parentPath)
	sanitised := SanitiseBranchName(branch)
	return filepath.Join(parentDir, parentName+"-worktrees", sanitised)
}

func CreateWorktree(parentPath, branch, baseRef string) (string, error) {
	wtPath := WorktreePath(parentPath, branch)

	if err := os.MkdirAll(filepath.Dir(wtPath), 0755); err != nil {
		return "", fmt.Errorf("creating worktree parent dir: %w", err)
	}

	args := []string{"-C", parentPath, "worktree", "add"}
	if baseRef != "" {
		args = append(args, "-b", branch, wtPath, baseRef)
	} else {
		args = append(args, wtPath, branch)
	}

	cmd := exec.Command("git", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("creating worktree: %w", err)
	}
	return wtPath, nil
}

func RemoveWorktree(parentPath, wtPath string) error {
	cmd := exec.Command("git", "-C", parentPath, "worktree", "remove", wtPath)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func CreateSymlinks(sourcePath, targetPath string, names []string) error {
	for _, name := range names {
		src := filepath.Join(sourcePath, name)
		dst := filepath.Join(targetPath, name)

		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		}

		// Remove existing file/dir at destination if it exists
		os.Remove(dst)

		if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("creating symlink %s -> %s: %w", dst, src, err)
		}
	}
	return nil
}

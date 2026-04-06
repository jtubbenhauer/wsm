package plans

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const planDir = ".opencode/plans"

type PlanFile struct {
	Name    string
	Path    string
	ModTime time.Time
}

func Discover(workspacePath string) ([]PlanFile, error) {
	dir := filepath.Join(workspacePath, planDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []PlanFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, PlanFile{
			Name:    entry.Name(),
			Path:    filepath.Join(dir, entry.Name()),
			ModTime: info.ModTime(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	return files, nil
}

func PickBest(files []PlanFile, workspaceName string) *PlanFile {
	if len(files) == 0 {
		return nil
	}

	slug := strings.ToLower(strings.ReplaceAll(workspaceName, " ", "-"))
	// strip parent path components — "org/repo" → "repo"
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		slug = slug[i+1:]
	}

	for i := range files {
		nameLower := strings.ToLower(files[i].Name)
		if strings.Contains(nameLower, slug) {
			return &files[i]
		}
	}

	return &files[0]
}

package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID        string
	Directory string
	FilePath  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type sessionHeader struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

func sessionsBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	return filepath.Join(home, ".pi", "agent", "sessions"), nil
}

// EncodePath converts a directory path to Pi's session directory encoding.
// /home/jack/dev/wsm -> --home-jack-dev-wsm--
func EncodePath(dir string) string {
	encoded := strings.ReplaceAll(dir, "/", "-")
	return "-" + encoded + "--"
}

func sessionDirForPath(directory string) (string, error) {
	base, err := sessionsBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, EncodePath(directory)), nil
}

// ListSessions returns all sessions for a given workspace directory.
func ListSessions(directory string) ([]Session, error) {
	dir, err := sessionDirForPath(directory)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading session dir %s: %w", dir, err)
	}

	var sessions []Session
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		session, err := parseSessionFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		sessions = append(sessions, *session)
	}
	return sessions, nil
}

// FetchSessionsForDirs returns sessions grouped by directory path.
func FetchSessionsForDirs(dirs []string) (map[string][]Session, error) {
	grouped := make(map[string][]Session)
	for _, dir := range dirs {
		sessions, err := ListSessions(dir)
		if err != nil {
			continue
		}
		if len(sessions) > 0 {
			grouped[dir] = sessions
		}
	}
	return grouped, nil
}

// DeleteSession removes the JSONL file for a given session ID within a directory.
func DeleteSession(directory, sessionID string) error {
	dir, err := sessionDirForPath(directory)
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading session dir: %w", err)
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") || !strings.Contains(entry.Name(), sessionID) {
			continue
		}
		return os.Remove(filepath.Join(dir, entry.Name()))
	}
	return fmt.Errorf("session %s not found in %s", sessionID, dir)
}

// FindSessionFile returns the JSONL file path for a given session ID.
func FindSessionFile(directory, sessionID string) (string, error) {
	dir, err := sessionDirForPath(directory)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading session dir: %w", err)
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") || !strings.Contains(entry.Name(), sessionID) {
			continue
		}
		return filepath.Join(dir, entry.Name()), nil
	}
	return "", fmt.Errorf("session %s not found in %s", sessionID, dir)
}

// CreateSession creates a new Pi session JSONL file and returns it.
func CreateSession(directory string) (*Session, error) {
	dir, err := sessionDirForPath(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating session dir: %w", err)
	}

	now := time.Now().UTC()
	id := uuid.New().String()
	ts := now.Format("2006-01-02T15-04-05-000Z")
	filePath := filepath.Join(dir, fmt.Sprintf("%s_%s.jsonl", ts, id))

	header := sessionHeader{
		Type:      "session",
		Version:   3,
		ID:        id,
		Timestamp: now.Format(time.RFC3339Nano),
		CWD:       directory,
	}
	data, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshalling session header: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return nil, fmt.Errorf("writing session file: %w", err)
	}

	return &Session{
		ID:        id,
		Directory: directory,
		FilePath:  filePath,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func parseSessionFile(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty session file: %s", path)
	}

	var header sessionHeader
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return nil, fmt.Errorf("parsing session header: %w", err)
	}
	if header.Type != "session" {
		return nil, fmt.Errorf("unexpected header type: %s", header.Type)
	}

	createdAt, err := time.Parse(time.RFC3339Nano, header.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("parsing timestamp: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	return &Session{
		ID:        header.ID,
		Directory: header.CWD,
		FilePath:  path,
		CreatedAt: createdAt,
		UpdatedAt: info.ModTime(),
	}, nil
}

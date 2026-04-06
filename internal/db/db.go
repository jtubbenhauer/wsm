package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type WorkspaceType string

const (
	WorkspaceTypeRepo     WorkspaceType = "repo"
	WorkspaceTypeWorktree WorkspaceType = "worktree"
)

type Workspace struct {
	ID         int64         `json:"id"`
	Name       string        `json:"name"`
	Path       string        `json:"path"`
	Type       WorkspaceType `json:"type"`
	ParentPath string        `json:"parentPath,omitempty"`
	Branch     string        `json:"branch,omitempty"`
	Symlinks   []string      `json:"symlinks,omitempty"`
	CreatedAt  time.Time     `json:"createdAt"`
}

type SessionActivity struct {
	WorkspaceID int64     `json:"workspaceId"`
	SessionID   string    `json:"sessionId"`
	LastFocused time.Time `json:"lastFocused"`
	Label       string    `json:"label,omitempty"`
}

type DB struct {
	conn *sql.DB
}

func dbPath() (string, error) {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting home dir: %w", err)
		}
		dataDir = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(dataDir, "wsm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating data dir: %w", err)
	}
	return filepath.Join(dir, "wsm.db"), nil
}

func Open() (*DB, error) {
	path, err := dbPath()
	if err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS workspaces (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL UNIQUE,
			path        TEXT NOT NULL UNIQUE,
			type        TEXT NOT NULL DEFAULT 'repo',
			parent_path TEXT,
			branch      TEXT,
			symlinks    TEXT DEFAULT '[]',
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS session_activity (
			workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			session_id   TEXT NOT NULL,
			last_focused DATETIME NOT NULL,
			label        TEXT,
			PRIMARY KEY (workspace_id, session_id)
		)`,
	}
	for _, m := range migrations {
		if _, err := db.conn.Exec(m); err != nil {
			return fmt.Errorf("executing migration: %w", err)
		}
	}
	return nil
}

func (db *DB) AddWorkspace(name, path string, wsType WorkspaceType, parentPath, branch string, symlinks []string) (*Workspace, error) {
	if symlinks == nil {
		symlinks = []string{}
	}
	symlinksJSON, err := json.Marshal(symlinks)
	if err != nil {
		return nil, fmt.Errorf("marshaling symlinks: %w", err)
	}

	result, err := db.conn.Exec(
		`INSERT INTO workspaces (name, path, type, parent_path, branch, symlinks) VALUES (?, ?, ?, ?, ?, ?)`,
		name, path, string(wsType), nullString(parentPath), nullString(branch), string(symlinksJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("inserting workspace: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("getting last insert id: %w", err)
	}

	return &Workspace{
		ID:         id,
		Name:       name,
		Path:       path,
		Type:       wsType,
		ParentPath: parentPath,
		Branch:     branch,
		Symlinks:   symlinks,
		CreatedAt:  time.Now(),
	}, nil
}

func (db *DB) GetWorkspace(name string) (*Workspace, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, path, type, parent_path, branch, symlinks, created_at FROM workspaces WHERE name = ?`,
		name,
	)
	return scanWorkspace(row)
}

func (db *DB) GetWorkspaceByPath(path string) (*Workspace, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, path, type, parent_path, branch, symlinks, created_at FROM workspaces WHERE path = ?`,
		path,
	)
	return scanWorkspace(row)
}

func (db *DB) ListWorkspaces() ([]Workspace, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, path, type, parent_path, branch, symlinks, created_at FROM workspaces ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying workspaces: %w", err)
	}
	defer rows.Close()

	var workspaces []Workspace
	for rows.Next() {
		ws, err := scanWorkspaceRow(rows)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, *ws)
	}
	return workspaces, rows.Err()
}

func (db *DB) RemoveWorkspace(name string) error {
	result, err := db.conn.Exec(`DELETE FROM workspaces WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("deleting workspace: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("workspace %q not found", name)
	}
	return nil
}

func (db *DB) UpsertSessionActivity(workspaceID int64, sessionID string, label string) error {
	_, err := db.conn.Exec(
		`INSERT INTO session_activity (workspace_id, session_id, last_focused, label)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(workspace_id, session_id)
		 DO UPDATE SET last_focused = excluded.last_focused, label = excluded.label`,
		workspaceID, sessionID, time.Now().UTC(), nullString(label),
	)
	if err != nil {
		return fmt.Errorf("upserting session activity: %w", err)
	}
	return nil
}

func (db *DB) GetSessionActivities(workspaceID int64) ([]SessionActivity, error) {
	rows, err := db.conn.Query(
		`SELECT workspace_id, session_id, last_focused, label
		 FROM session_activity WHERE workspace_id = ? ORDER BY last_focused DESC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying session activities: %w", err)
	}
	defer rows.Close()

	var activities []SessionActivity
	for rows.Next() {
		var sa SessionActivity
		var label sql.NullString
		if err := rows.Scan(&sa.WorkspaceID, &sa.SessionID, &sa.LastFocused, &label); err != nil {
			return nil, fmt.Errorf("scanning session activity: %w", err)
		}
		sa.Label = label.String
		activities = append(activities, sa)
	}
	return activities, rows.Err()
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanWorkspace(row scanner) (*Workspace, error) {
	var ws Workspace
	var parentPath, branch sql.NullString
	var symlinksJSON string

	if err := row.Scan(&ws.ID, &ws.Name, &ws.Path, &ws.Type, &parentPath, &branch, &symlinksJSON, &ws.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning workspace: %w", err)
	}

	ws.ParentPath = parentPath.String
	ws.Branch = branch.String
	if err := json.Unmarshal([]byte(symlinksJSON), &ws.Symlinks); err != nil {
		return nil, fmt.Errorf("unmarshaling symlinks: %w", err)
	}
	return &ws, nil
}

func scanWorkspaceRow(rows *sql.Rows) (*Workspace, error) {
	return scanWorkspace(rows)
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

package opencode

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = 4096
)

type Session struct {
	ID        string         `json:"id"`
	Slug      string         `json:"slug"`
	ProjectID string         `json:"projectID"`
	Directory string         `json:"directory"`
	Title     string         `json:"title"`
	ParentID  string         `json:"parentID,omitempty"`
	Version   string         `json:"version"`
	Summary   SessionSummary `json:"summary"`
	Time      SessionTime    `json:"time"`
}

type SessionSummary struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Files     int `json:"files"`
}

type SessionTime struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

type SessionStatus struct {
	Type    string `json:"type"` // "idle", "busy", "retry"
	Attempt int    `json:"attempt,omitempty"`
	Message string `json:"message,omitempty"`
	Next    int64  `json:"next,omitempty"`
}

func (s *SessionStatus) IsBusy() bool {
	return s.Type == "busy"
}

func (s *SessionStatus) IsRetry() bool {
	return s.Type == "retry"
}

func (s *Session) CreatedAt() time.Time {
	return time.UnixMilli(s.Time.Created)
}

func (s *Session) UpdatedAt() time.Time {
	return time.UnixMilli(s.Time.Updated)
}

func (s *Session) IsTopLevel() bool {
	return s.ParentID == ""
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(host string, port int) *Client {
	return &Client{
		baseURL:    fmt.Sprintf("http://%s:%d", host, port),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func DefaultClient() *Client {
	return NewClient(DefaultHost, DefaultPort)
}

func (c *Client) IsServerRunning() bool {
	resp, err := c.httpClient.Get(c.baseURL + "/session")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Client) ListSessions(directory string) ([]Session, error) {
	endpoint := c.baseURL + "/session"
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}

	resp, err := c.httpClient.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing sessions: HTTP %d", resp.StatusCode)
	}

	var sessions []Session
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, fmt.Errorf("decoding sessions: %w", err)
	}
	return sessions, nil
}

func (c *Client) ListTopLevelSessions(directory string) ([]Session, error) {
	all, err := c.ListSessions(directory)
	if err != nil {
		return nil, err
	}
	var topLevel []Session
	for _, s := range all {
		if s.IsTopLevel() {
			topLevel = append(topLevel, s)
		}
	}
	return topLevel, nil
}

// FetchSessionsForDirs fetches top-level sessions for multiple directories in parallel
func (c *Client) FetchSessionsForDirs(dirs []string) (map[string][]Session, error) {
	type result struct {
		dir      string
		sessions []Session
		err      error
	}

	results := make(chan result, len(dirs))
	var wg sync.WaitGroup

	for _, dir := range dirs {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			sessions, err := c.ListTopLevelSessions(d)
			results <- result{dir: d, sessions: sessions, err: err}
		}(dir)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	grouped := make(map[string][]Session)
	var firstErr error
	for r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("fetching sessions for %s: %w", r.dir, r.err)
			}
			continue
		}
		if len(r.sessions) > 0 {
			grouped[r.dir] = r.sessions
		}
	}

	if firstErr != nil && len(grouped) == 0 {
		return nil, firstErr
	}
	return grouped, nil
}

func (c *Client) GetSessionStatuses(directory string) (map[string]SessionStatus, error) {
	endpoint := c.baseURL + "/session/status"
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}

	resp, err := c.httpClient.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("getting session statuses: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getting session statuses: HTTP %d", resp.StatusCode)
	}

	var statuses map[string]SessionStatus
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return nil, fmt.Errorf("decoding session statuses: %w", err)
	}
	return statuses, nil
}

// FetchStatusesForDirs fetches session statuses for multiple directories in parallel.
// Returns a flat map of sessionID → SessionStatus. Sessions absent from the API response are idle.
func (c *Client) FetchStatusesForDirs(dirs []string) map[string]SessionStatus {
	type result struct {
		statuses map[string]SessionStatus
	}

	results := make(chan result, len(dirs))
	var wg sync.WaitGroup

	for _, dir := range dirs {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			statuses, err := c.GetSessionStatuses(d)
			if err != nil {
				results <- result{}
				return
			}
			results <- result{statuses: statuses}
		}(dir)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	merged := make(map[string]SessionStatus)
	for r := range results {
		for id, status := range r.statuses {
			merged[id] = status
		}
	}
	return merged
}

func (c *Client) CreateSession(directory string) (*Session, error) {
	endpoint := c.baseURL + "/session"
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}

	resp, err := c.httpClient.Post(endpoint, "application/json", strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("creating session: HTTP %d", resp.StatusCode)
	}

	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("decoding session: %w", err)
	}
	return &session, nil
}

func (c *Client) DeleteSession(sessionID string) error {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+"/session/"+sessionID, nil)
	if err != nil {
		return fmt.Errorf("creating delete request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deleting session: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func OpencodeBinary() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "opencode"
	}
	binPath := filepath.Join(home, ".opencode", "bin", "opencode")
	if _, err := os.Stat(binPath); err == nil {
		return binPath
	}
	return "opencode"
}

func EnsureServer(host string, port int) (*Client, error) {
	client := NewClient(host, port)
	if client.IsServerRunning() {
		return client, nil
	}

	bin := OpencodeBinary()
	cmd := exec.Command(bin, "serve", "--hostname="+host, fmt.Sprintf("--port=%d", port))
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting opencode server: %w", err)
	}

	// Wait for server to become ready
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if client.IsServerRunning() {
			return client, nil
		}
	}
	return nil, fmt.Errorf("opencode server did not start within 3s")
}

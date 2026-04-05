package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const localSessionsDir = "data/sessions"

// loadPersistedSessions 从 data/sessions 目录恢复本地会话。
func (s *sessionStore) loadPersistedSessions() error {
	files, err := s.findPersistedSessionFiles()
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range files {
		session, err := s.readPersistedSessionFile(path)
		if err != nil || session == nil {
			continue
		}
		session.IsRunning = false
		touchSessionUpdatedAt(session)
		s.sessions[session.ID] = &sessionRuntime{
			session: session,
			clients: map[*clientConn]struct{}{},
		}
	}
	return nil
}

// persistSessions 把当前本地会话写入 data/sessions 目录，并清理已删除的旧文件。
func (s *sessionStore) persistSessions() error {
	s.mu.RLock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, rt := range s.sessions {
		if rt == nil || rt.session == nil || !isLocalSessionID(rt.session.ID) {
			continue
		}
		session := cloneSession(rt.session)
		if s.appConfig != nil && !s.appConfig.PersistEvents {
			session.Events = []*Event{}
		}
		sessions = append(sessions, session)
	}
	s.mu.RUnlock()

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	if err := os.MkdirAll(localSessionsDir, 0o755); err != nil {
		return err
	}

	keep := map[string]struct{}{}
	for _, session := range sessions {
		if session == nil {
			continue
		}
		path := sessionFilePath(session.Provider, session.ID)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		data, err := json.MarshalIndent(session, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		keep[filepath.Clean(path)] = struct{}{}
	}

	files, err := s.findPersistedSessionFiles()
	if err != nil {
		return err
	}
	for _, path := range files {
		cleanPath := filepath.Clean(path)
		if _, ok := keep[cleanPath]; ok {
			continue
		}
		_ = os.Remove(cleanPath)
	}
	return nil
}

// readPersistedSession 根据 provider 和 session ID 读取单个本地会话。
func (s *sessionStore) readPersistedSession(provider, sessionID string) (*Session, error) {
	path := sessionFilePath(provider, sessionID)
	return s.readPersistedSessionFile(path)
}

// deletePersistedSession 删除单个本地会话文件。
func (s *sessionStore) deletePersistedSession(provider, sessionID string) error {
	path := sessionFilePath(provider, sessionID)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// findPersistedSessionFiles 扫描 data/sessions 下所有 provider 目录中的会话文件。
func (s *sessionStore) findPersistedSessionFiles() ([]string, error) {
	files := make([]string, 0, 64)
	err := filepath.WalkDir(localSessionsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return files, nil
}

// readPersistedSessionFile 从单个 json 文件中读取会话内容。
func (s *sessionStore) readPersistedSessionFile(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	if !isLocalSessionID(session.ID) {
		return nil, errors.New("invalid local session id")
	}
	if strings.TrimSpace(session.Provider) == "" {
		session.Provider = filepath.Base(filepath.Dir(path))
	}
	session.IsRunning = false
	touchSessionUpdatedAt(&session)
	return cloneSession(&session), nil
}

// sessionFilePath 返回本地会话对应的持久化文件路径。
func sessionFilePath(provider, sessionID string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "claude"
	}
	sessionID = filepath.Base(strings.TrimSpace(sessionID))
	return filepath.Join(localSessionsDir, provider, sessionID+".json")
}

// isLocalSessionID 判断是否为本地 web 会话 ID。
func isLocalSessionID(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	if strings.Contains(sessionID, "/") || strings.Contains(sessionID, "\\") {
		return false
	}
	return true
}

// touchSessionUpdatedAt 为零值更新时间补上当前时间，避免排序异常。
func touchSessionUpdatedAt(session *Session) {
	if session == nil {
		return
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = time.Now()
	}
}

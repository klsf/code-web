package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	urlPattern         = regexp.MustCompile(`https?://[^\s"'<>]+`)
	loginServerPattern = regexp.MustCompile(`https?://localhost:\d+`)
	ansiRegex          = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

var authGuideSteps = []string{
	"点击下面按钮，会在新页面打开 ChatGPT 登录授权。",
	"在新页面完成授权，浏览器会跳转到一个 http://localhost:1455/auth/callback?... 链接。",
	"复制完整回调链接，回到当前页面粘贴，然后点击“完成授权”。",
}

const codexAuthSessionTTL = 15 * time.Minute
const codexLoginStatusCacheTTL = 3 * time.Second
const codexAuthStartWait = 5 * time.Second

func newCodexAuthManager() *codexAuthManager {
	return &codexAuthManager{}
}

func (m *codexAuthManager) invalidateLoginStatusCacheLocked() {
	m.loginCheckedAt = time.Time{}
	m.loginStatusCached = false
	m.loginMessageCached = ""
}

func (m *codexAuthManager) loginStatus(force bool) (bool, string) {
	m.mu.Lock()
	if !force && !m.loginCheckedAt.IsZero() && time.Since(m.loginCheckedAt) < codexLoginStatusCacheTTL {
		loggedIn := m.loginStatusCached
		message := m.loginMessageCached
		m.mu.Unlock()
		return loggedIn, message
	}
	m.mu.Unlock()

	loggedIn, message := codexLoginStatus()

	m.mu.Lock()
	m.loginCheckedAt = time.Now()
	m.loginStatusCached = loggedIn
	m.loginMessageCached = message
	m.mu.Unlock()
	return loggedIn, message
}

func stripANSI(text string) string {
	return ansiRegex.ReplaceAllString(text, "")
}

func trimDetectedURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), ".,);]}>")
}

func extractAuthURL(text string) (string, string) {
	for _, raw := range urlPattern.FindAllString(text, -1) {
		candidate := trimDetectedURL(raw)
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
		if host == "" || host == "localhost" || host == "127.0.0.1" {
			continue
		}
		query := parsed.Query()
		state := strings.TrimSpace(query.Get("state"))
		if state == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(query.Get("response_type")), "code") ||
			strings.TrimSpace(query.Get("code_challenge")) != "" ||
			strings.Contains(strings.ToLower(parsed.Path), "/oauth/authorize") {
			return candidate, state
		}
	}
	return "", ""
}

func extractCallbackURL(text string) string {
	for _, raw := range urlPattern.FindAllString(text, -1) {
		candidate := trimDetectedURL(raw)
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
		if host != "localhost" && host != "127.0.0.1" {
			continue
		}
		parsed.RawQuery = ""
		parsed.Fragment = ""
		if parsed.Path == "" || parsed.Path == "/" {
			parsed.Path = "/auth/callback"
			return parsed.String()
		}
		if strings.HasSuffix(parsed.Path, "/auth/callback") {
			return parsed.String()
		}
	}
	if match := loginServerPattern.FindString(text); match != "" {
		return trimDetectedURL(match) + "/auth/callback"
	}
	return ""
}

func populateAuthSessionFromText(session *codexAuthSession, text string) {
	if session == nil {
		return
	}
	if session.AuthURL == "" {
		if authURL, state := extractAuthURL(text); authURL != "" {
			session.AuthURL = authURL
			if session.State == "" {
				session.State = state
			}
		}
	}
	if session.Callback == "" {
		if callback := extractCallbackURL(text); callback != "" {
			session.Callback = callback
		}
	}
	if session.AuthURL != "" && session.Status == "pending" {
		session.Status = "ready"
	}
}

func codexLoginStatus() (bool, string) {
	cmd := exec.Command("codex", "login", "status")
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(stripANSI(string(output)))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return false, text
	}
	return strings.Contains(strings.ToLower(text), "logged in"), text
}

func isCodexAuthError(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(text, "not logged in") ||
		strings.Contains(text, "codex login") ||
		strings.Contains(text, "authentication") ||
		strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "login required") ||
		strings.Contains(text, "logged out") ||
		strings.Contains(text, "expired")
}

func authStatusMessage(message string) string {
	text := strings.TrimSpace(stripANSI(message))
	lower := strings.ToLower(text)
	switch {
	case text == "":
		return ""
	case strings.Contains(lower, "state mismatch"):
		return "这条回调链接不属于当前这次授权，请重新点击“打开授权页面”后再完成一次。"
	case strings.Contains(lower, "operation timed out"), strings.Contains(lower, "context deadline exceeded"):
		return "本次授权已超时，请重新点击“打开授权页面”。"
	case strings.Contains(lower, "callback url is required"):
		return "请先粘贴授权完成后的回调链接。"
	case strings.Contains(lower, "missing required authorization parameters"):
		return "回调链接不完整，请确认包含 code 和 state 参数。"
	case strings.Contains(lower, "port 127.0.0.1:1455 is already in use"):
		return "本机登录端口已被占用，请关闭旧的授权流程后重试。"
	case strings.Contains(lower, "not ready"):
		return "当前授权会话还没准备好，请重新点击“打开授权页面”。"
	case strings.Contains(lower, "invalid callback url"):
		return "回调链接格式不正确，请粘贴完整链接。"
	}
	return text
}

func isExpiredCodexAuthSession(session *codexAuthSession) bool {
	if session == nil {
		return false
	}
	if session.Status != "pending" && session.Status != "ready" {
		return false
	}
	return time.Since(session.StartedAt) > codexAuthSessionTTL
}

func newCodexAuthID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("auth-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", buf[:])
}

func newCodexAuthSession() *codexAuthSession {
	return &codexAuthSession{
		ID:        newCodexAuthID(),
		StartedAt: time.Now(),
		Status:    "pending",
	}
}

func (m *codexAuthManager) stopLocked() {
	if m.proc != nil && m.proc.Process != nil {
		_ = m.proc.Process.Kill()
	}
	m.proc = nil
}

func (m *codexAuthManager) expireLocked() {
	if !isExpiredCodexAuthSession(m.session) {
		return
	}
	m.stopLocked()
	m.session.Status = "failed"
	m.session.Error = authStatusMessage("operation timed out")
	authLog.Warn().Str("session_id", m.session.ID).Msg("auth session expired")
}

func (m *codexAuthManager) Status() codexAuthStatusResponse {
	loggedIn, _ := m.loginStatus(false)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if loggedIn {
		return codexAuthStatusResponse{LoggedIn: true, Session: m.session}
	}
	if m.session == nil {
		return codexAuthStatusResponse{LoggedIn: false}
	}
	copySession := *m.session
	copySession.Error = authStatusMessage(copySession.Error)
	return codexAuthStatusResponse{LoggedIn: false, Session: &copySession}
}

func (m *codexAuthManager) EnsureStarted(ctx context.Context, restart bool) codexAuthStatusResponse {
	m.mu.Lock()
	m.expireLocked()
	if restart {
		m.stopLocked()
		m.session = nil
		m.invalidateLoginStatusCacheLocked()
	}
	if m.session != nil && (m.session.Status == "pending" || m.session.Status == "ready") {
		copySession := *m.session
		copySession.Error = authStatusMessage(copySession.Error)
		m.mu.Unlock()
		return codexAuthStatusResponse{LoggedIn: false, Session: &copySession}
	}

	session := newCodexAuthSession()
	m.session = session
	m.mu.Unlock()
	authLog.Info().Str("session_id", session.ID).Bool("restart", restart).Msg("auth session started")

	cmd := exec.Command("codex", "login")
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	var transcript bytes.Buffer
	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		session.Status = "failed"
		session.Error = authStatusMessage(err.Error())
		copySession := *session
		m.mu.Unlock()
		return codexAuthStatusResponse{LoggedIn: false, Session: &copySession}
	}
	m.mu.Lock()
	m.proc = cmd
	m.mu.Unlock()

	var streamWG sync.WaitGroup
	streamWG.Add(2)
	go func() {
		defer streamWG.Done()
		m.captureAuthStream(session, stdout, &transcript)
	}()
	go func() {
		defer streamWG.Done()
		m.captureAuthStream(session, stderr, &transcript)
	}()
	go func() {
		err := cmd.Wait()
		streamWG.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.proc == cmd {
			m.proc = nil
		}
		if session.Status != "pending" && session.Status != "ready" {
			return
		}
		if err == nil {
			if loggedIn, _ := m.loginStatus(true); loggedIn {
				session.Status = "complete"
				authLog.Info().Str("session_id", session.ID).Msg("auth session completed")
				return
			}
		}
		session.Status = "failed"
		session.Error = authStatusMessage(strings.TrimSpace(stripANSI(transcript.String())))
		if session.Error == "" && err != nil {
			session.Error = authStatusMessage(err.Error())
		}
		authLog.Error().Str("session_id", session.ID).Str("error", session.Error).Msg("auth session failed")
	}()

	deadline := time.Now().Add(codexAuthStartWait)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		populateAuthSessionFromText(session, transcript.String())
		ready := session.AuthURL != ""
		copySession := *session
		m.mu.Unlock()
		if ready {
			copySession.Status = "ready"
			authLog.Info().Str("session_id", copySession.ID).Msg("auth session ready")
			return codexAuthStatusResponse{LoggedIn: false, Session: &copySession}
		}
		time.Sleep(100 * time.Millisecond)
	}

	return m.Status()
}

func (m *codexAuthManager) captureAuthStream(session *codexAuthSession, r io.Reader, transcript *bytes.Buffer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(stripANSI(scanner.Text()))
		if line == "" {
			continue
		}
		transcript.WriteString(line + "\n")
		m.mu.Lock()
		populateAuthSessionFromText(session, line)
		m.mu.Unlock()
	}
}

func codexAuthRestart(raw string) bool {
	text := strings.TrimSpace(strings.ToLower(raw))
	return text == "1" || text == "true" || text == "yes"
}

func codexAuthProxyTarget() string {
	return "http://" + net.JoinHostPort("127.0.0.1", "1455")
}

func completeCodexAuth(ctx context.Context, raw string) error {
	text := strings.TrimSpace(raw)
	if text == "" {
		return errors.New("callback url is required")
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return fmt.Errorf("invalid callback url: %w", err)
	}
	values := parsed.Query()
	code := strings.TrimSpace(values.Get("code"))
	state := strings.TrimSpace(values.Get("state"))
	scope := strings.TrimSpace(values.Get("scope"))
	if code == "" || state == "" {
		return errors.New("callback url is missing required authorization parameters")
	}
	query := url.Values{}
	query.Set("code", code)
	query.Set("state", state)
	if scope != "" {
		query.Set("scope", scope)
	}
	target := codexAuthProxyTarget() + "/auth/callback?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return errors.New(msg)
	}
	return nil
}

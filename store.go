package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type sessionRuntime struct {
	session *Session
	clients map[*clientConn]struct{}
}

type clientConn struct {
	conn      *websocket.Conn
	send      chan serverEvent
	closeOnce sync.Once
	writeMu   sync.Mutex
}

type sessionStore struct {
	mu           sync.RWMutex
	sessions     map[string]*sessionRuntime
	providers    map[string]Provider
	appConfig    *appConfig
	authPassword string
	authToken    string
}

type restoreRef struct {
	Provider          string `json:"provider"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
}

type sessionListItem struct {
	ID                string      `json:"id"`
	Provider          string      `json:"provider"`
	Model             string      `json:"model,omitempty"`
	Title             string      `json:"title,omitempty"`
	Workdir           string      `json:"workdir,omitempty"`
	ProviderSessionID string      `json:"providerSessionId,omitempty"`
	UpdatedAt         time.Time   `json:"updatedAt"`
	MessageCount      int         `json:"messageCount"`
	Running           bool        `json:"running"`
	LastMessage       string      `json:"lastMessage,omitempty"`
	LastEvent         string      `json:"lastEvent,omitempty"`
	RestoreRef        *restoreRef `json:"restoreRef,omitempty"`
}

// newSessionStore 创建包含 Claude 和 Codex 的最小会话存储。
func newSessionStore(password string) *sessionStore {
	cfg := loadAppConfig()
	store := &sessionStore{
		sessions: map[string]*sessionRuntime{},
		providers: map[string]Provider{
			"claude": &ClaudeProvider{},
			"codex":  &CodexProvider{},
		},
		appConfig:    cfg,
		authPassword: strings.TrimSpace(password),
		authToken:    authTokenForPassword(password),
	}
	if err := store.loadPersistedSessions(); err != nil {
		// 持久化文件损坏时不阻断启动，后续仍可继续使用新会话。
	}
	return store
}

// newUUID 生成标准 UUID，用于会话、消息、事件和上传文件标识。
func newUUID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4],
		buf[4:6],
		buf[6:8],
		buf[8:10],
		buf[10:16],
	)
}

func (s *sessionStore) handleIndex(staticFS fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			withCache(http.FileServer(http.FS(staticFS))).ServeHTTP(w, r)
			return
		}
		content, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			http.Error(w, "index file not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(content)
	}
}

// handleAppConfig 向前端暴露当前最小应用配置。
func (s *sessionStore) handleAppConfig(w http.ResponseWriter, _ *http.Request) {
	defaultProvider := s.defaultProviderID()
	defaultModel := s.defaultModelForProvider(defaultProvider)
	providers := make([]map[string]any, 0, len(s.appConfig.Providers))
	for _, item := range s.appConfig.Providers {
		if item == nil {
			continue
		}
		providers = append(providers, map[string]any{
			"id":           item.ID,
			"name":         item.Name,
			"displayName":  item.Name,
			"available":    item.Available,
			"isDefault":    item.IsDefault,
			"defaultModel": item.DefaultModel,
			"models":       append([]string(nil), item.Models...),
		})
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte("window.__APP_CONFIG = " + mustJSON(map[string]any{
		"provider":  defaultProvider,
		"model":     defaultModel,
		"version":   appVersion(),
		"appName":   s.appConfig.AppName,
		"providers": providers,
	}) + ";\n"))
}

// handleLogin 校验登录密码并设置会话 cookie。
func (s *sessionStore) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Password) != s.authPassword {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "code_web_auth",
		Value:    s.authToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAuth 返回当前请求是否已经登录。
func (s *sessionStore) handleAuth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": s.isAuthenticated(r)})
}

// handleLogout 清理登录 cookie。
func (s *sessionStore) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "code_web_auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleStatus 返回当前服务的基础状态信息。
func (s *sessionStore) handleStatus(w http.ResponseWriter, _ *http.Request) {
	defaultProvider := s.defaultProviderID()
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": defaultProvider,
		"model":    s.defaultModelForProvider(defaultProvider),
	})
}

// handleSessions 返回当前本地会话列表。
func (s *sessionStore) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := s.listSessions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleRestoreSession 根据本地持久化会话恢复会话内容。
func (s *sessionStore) handleRestoreSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Provider   string      `json:"provider"`
		SessionID  string      `json:"sessionId"`
		RestoreRef *restoreRef `json:"restoreRef"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	providerID := strings.TrimSpace(req.Provider)
	remoteID := strings.TrimSpace(req.SessionID)
	if req.RestoreRef != nil {
		if providerID == "" {
			providerID = strings.TrimSpace(req.RestoreRef.Provider)
		}
		if remoteID == "" {
			remoteID = strings.TrimSpace(req.RestoreRef.ProviderSessionID)
		}
	}
	if remoteID == "" {
		http.Error(w, "missing local session id", http.StatusBadRequest)
		return
	}
	if !isLocalSessionID(remoteID) {
		http.Error(w, "only local sessions can be restored", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	if rt, ok := s.sessions[remoteID]; ok && rt != nil && rt.session != nil {
		s.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": rt.session.ID})
		return
	}
	s.mu.RUnlock()

	session, err := s.readPersistedSession(providerID, remoteID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	s.mu.Lock()
	s.sessions[session.ID] = &sessionRuntime{
		session: session,
		clients: map[*clientConn]struct{}{},
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"sessionId": session.ID})
}

// handleDeleteSession 删除本地会话。
func (s *sessionStore) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID         string      `json:"id"`
		Provider   string      `json:"provider"`
		RestoreRef *restoreRef `json:"restoreRef"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	sessionID := strings.TrimSpace(req.ID)
	providerID := strings.TrimSpace(req.Provider)
	if req.RestoreRef != nil {
		if sessionID == "" {
			sessionID = strings.TrimSpace(req.RestoreRef.ProviderSessionID)
		}
		if providerID == "" {
			providerID = strings.TrimSpace(req.RestoreRef.Provider)
		}
	}
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if rt.session.IsRunning {
		s.mu.Unlock()
		http.Error(w, "当前会话仍在生成中，请稍候", http.StatusBadRequest)
		return
	}
	delete(s.sessions, sessionID)
	s.mu.Unlock()
	for client := range rt.clients {
		closeClientConn(client)
	}
	if err := s.deletePersistedSession(firstNonEmptyString(providerID, rt.session.Provider), sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.persistSessions()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleNewSession 创建一个新的聊天会话。
func (s *sessionStore) handleNewSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Workdir  string `json:"workdir"`
		Provider string `json:"provider"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	provider := s.providerByName(req.Provider)
	if provider == nil {
		http.Error(w, "unsupported provider", http.StatusBadRequest)
		return
	}

	session := &Session{
		ID:                newUUID(),
		ProviderSessionID: "",
		Provider:          provider.Name(),
		Model:             s.defaultModelForProvider(provider.Name()),
		Workdir:           normalizeWorkdir(req.Workdir),
		Messages:          []*Message{},
		Events:            []*Event{},
		IsRunning:         false,
		UpdatedAt:         time.Now(),
	}

	s.mu.Lock()
	s.sessions[session.ID] = &sessionRuntime{
		session: session,
		clients: map[*clientConn]struct{}{},
	}
	s.mu.Unlock()
	_ = s.persistSessions()

	writeJSON(w, http.StatusOK, map[string]string{"sessionId": session.ID})
}

// handleSend 接收前端发送的消息并触发异步生成。
func (s *sessionStore) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	sessionID := strings.TrimSpace(r.FormValue("sessionId"))
	content := strings.TrimSpace(r.FormValue("content"))
	model := strings.TrimSpace(r.FormValue("model"))
	if sessionID == "" {
		http.Error(w, "missing sessionId", http.StatusBadRequest)
		return
	}
	imageIDs, err := s.saveMultipartImages(r.MultipartForm.File["images"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if content == "" && len(imageIDs) == 0 {
		http.Error(w, "message is empty", http.StatusBadRequest)
		return
	}
	if err := s.enqueuePrompt(sessionID, content, model, imageIDs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleWS 建立 WebSocket 连接，并向前端同步会话快照和增量消息。
func (s *sessionStore) handleWS(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var hello clientEvent
	if err := conn.ReadJSON(&hello); err != nil {
		return
	}
	if hello.Type != "hello" {
		_ = conn.WriteJSON(serverEvent{Type: "error", Error: "first message must be hello"})
		return
	}

	rt, client, err := s.attachClient(hello.SessionID, conn)
	if err != nil {
		_ = conn.WriteJSON(serverEvent{Type: "error", Error: err.Error()})
		return
	}
	defer s.detachClient(rt.session.ID, client)

	_ = writeClientJSON(client, serverEvent{
		Type:    "snapshot",
		Session: cloneSession(rt.session),
		Running: rt.session.IsRunning,
	})

	for {
		var event clientEvent
		if err := conn.ReadJSON(&event); err != nil {
			return
		}
		if event.Type == "ping" {
			_ = writeClientJSON(client, serverEvent{Type: "pong"})
		}
	}
}

// attachClient 把一个 WebSocket 客户端挂到指定会话上。
func (s *sessionStore) attachClient(sessionID string, conn *websocket.Conn) (*sessionRuntime, *clientConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return nil, nil, errors.New("session not found")
	}

	client := &clientConn{conn: conn, send: make(chan serverEvent, 64)}
	rt.clients[client] = struct{}{}
	go startClientWriter(client)
	return rt, client, nil
}

// detachClient 把客户端从会话中移除并关闭连接。
func (s *sessionStore) detachClient(sessionID string, target *clientConn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	delete(rt.clients, target)
	closeClientConn(target)
}

// enqueuePrompt 把用户消息写入会话，并启动新的生成流程。
func (s *sessionStore) enqueuePrompt(sessionID, content, model string, imageIDs []string) error {
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return errors.New("session not found")
	}
	if rt.session.IsRunning {
		s.mu.Unlock()
		return errors.New("当前会话仍在生成中，请稍候")
	}
	imageURLs, imagePaths, err := resolveImageFiles(imageIDs)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if strings.TrimSpace(model) != "" {
		rt.session.Model = strings.TrimSpace(model)
	}

	now := time.Now()
	userMessage := &Message{ID: newUUID(), Role: "user", Content: content, ImageURLs: append([]string(nil), imageURLs...), CreatedAt: now}
	rt.session.Messages = append(rt.session.Messages, userMessage)
	rt.session.IsRunning = true
	rt.session.UpdatedAt = now
	sessionID = rt.session.ID
	model = rt.session.Model
	s.mu.Unlock()

	s.broadcast(sessionID, serverEvent{Type: "message", Message: userMessage})
	s.broadcast(sessionID, serverEvent{Type: "task_status", Running: true})
	_ = s.persistSessions()
	go s.runPrompt(sessionID, content, model, imagePaths)
	return nil
}

// runPrompt 调用当前会话对应的 CLI，并把结果回写到会话和前端。
func (s *sessionStore) runPrompt(sessionID, content, model string, imagePaths []string) {
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}

	draft := &Message{ID: newUUID(), Role: "assistant", Content: "", CreatedAt: time.Now()}
	rt.session.DraftMessage = draft
	sessionSnapshot := cloneSession(rt.session)
	if strings.TrimSpace(model) != "" {
		sessionSnapshot.Model = strings.TrimSpace(model)
	}
	s.mu.Unlock()

	s.broadcast(sessionID, serverEvent{Type: "message_delta", Message: cloneMessage(draft)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var execMu sync.Mutex
	var finishOnce sync.Once
	finishSession := func() {
		finishOnce.Do(func() {
			s.markSessionIdle(sessionID)
			s.broadcast(sessionID, serverEvent{Type: "task_status", Running: false})
		})
	}
	err := s.providerBySession(sessionSnapshot).Exec(ctx, sessionSnapshot, content, imagePaths, func(state *ProviderStateUpdate) {
		if state == nil {
			return
		}
		execMu.Lock()
		defer execMu.Unlock()
		s.updateProviderState(sessionID, state)
		if state.IsComplete {
			finishSession()
		}
	}, func(delta string) {
		if delta == "" {
			return
		}
		execMu.Lock()
		defer execMu.Unlock()
		s.appendAssistantDelta(sessionID, draft.ID, delta)
	}, func(text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		execMu.Lock()
		defer execMu.Unlock()
		s.completeAssistantMessage(sessionID, draft.ID, text)
		finishSession()
	}, func(event *Event) {
		if event == nil {
			return
		}
		execMu.Lock()
		defer execMu.Unlock()
		s.appendSessionEvent(sessionID, event)
	})
	if err != nil {
		s.failTask(sessionID, err)
		return
	}

	finishSession()
}

// saveMultipartImages 保存发送请求里附带的图片，并返回文件名列表。
func (s *sessionStore) saveMultipartImages(files []*multipart.FileHeader) ([]string, error) {
	imageIDs := make([]string, 0, len(files))
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			return nil, err
		}
		filename, err := saveUploadedFile(file, header)
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		imageIDs = append(imageIDs, filename)
	}
	return imageIDs, nil
}

// updateProviderState 把 provider 返回的会话状态写回当前本地会话。
func (s *sessionStore) updateProviderState(sessionID string, state *ProviderStateUpdate) {
	if state == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	if value := strings.TrimSpace(state.ProviderSessionID); value != "" {
		rt.session.ProviderSessionID = value
	}
	rt.session.UpdatedAt = time.Now()
	go func() { _ = s.persistSessions() }()
}

// failTask 在底层 CLI 执行失败时清理会话状态并广播错误。
func (s *sessionStore) failTask(sessionID string, err error) {
	message := "Claude 调用失败"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}

	s.mu.Lock()
	if rt, ok := s.sessions[sessionID]; ok {
		rt.session.IsRunning = false
		rt.session.DraftMessage = nil
		rt.session.UpdatedAt = time.Now()
	}
	s.mu.Unlock()

	s.broadcast(sessionID, serverEvent{Type: "error", Error: message})
	s.broadcast(sessionID, serverEvent{Type: "task_status", Running: false})
	_ = s.persistSessions()
}

// markSessionIdle 把会话切换回空闲状态，并刷新更新时间。
func (s *sessionStore) markSessionIdle(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rt, ok := s.sessions[sessionID]; ok {
		rt.session.IsRunning = false
		rt.session.UpdatedAt = time.Now()
	}
	go func() { _ = s.persistSessions() }()
}

// appendAssistantDelta 追加流式返回的增量文本，并推送给所有客户端。
func (s *sessionStore) appendAssistantDelta(sessionID, draftID, delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok || rt.session.DraftMessage == nil || rt.session.DraftMessage.ID != draftID {
		return
	}

	rt.session.DraftMessage.Content += delta
	rt.session.UpdatedAt = time.Now()
	s.broadcastLocked(rt, serverEvent{Type: "message_delta", Message: cloneMessage(rt.session.DraftMessage)})
	go func() { _ = s.persistSessions() }()
}

// completeAssistantMessage 把草稿消息转正为最终助手消息。
func (s *sessionStore) completeAssistantMessage(sessionID, draftID, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}

	if rt.session.DraftMessage != nil && rt.session.DraftMessage.ID == draftID {
		rt.session.DraftMessage.Content = text
		finalMessage := cloneMessage(rt.session.DraftMessage)
		rt.session.Messages = append(rt.session.Messages, finalMessage)
		rt.session.DraftMessage = nil
		rt.session.UpdatedAt = time.Now()
		s.broadcastLocked(rt, serverEvent{Type: "message_final", Message: cloneMessage(finalMessage)})
		go func() { _ = s.persistSessions() }()
		return
	}

	finalMessage := &Message{ID: newUUID(), Role: "assistant", Content: text, CreatedAt: time.Now()}
	rt.session.Messages = append(rt.session.Messages, finalMessage)
	rt.session.UpdatedAt = time.Now()
	s.broadcastLocked(rt, serverEvent{Type: "message_final", Message: cloneMessage(finalMessage)})
	go func() { _ = s.persistSessions() }()
}

// appendSessionEvent 追加会话事件，并把工具调用消息广播给所有客户端。
func (s *sessionStore) appendSessionEvent(sessionID string, event *Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok || event == nil {
		return
	}

	entry := cloneEvent(event)
	if strings.TrimSpace(entry.ID) == "" {
		entry.ID = newUUID()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	rt.session.Events = append(rt.session.Events, entry)
	rt.session.UpdatedAt = entry.CreatedAt
	s.broadcastLocked(rt, serverEvent{Type: "log", Log: cloneEvent(entry)})
	go func() { _ = s.persistSessions() }()
}

// broadcast 按会话广播事件给所有已连接客户端。
func (s *sessionStore) broadcast(sessionID string, event serverEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	for client := range rt.clients {
		select {
		case client.send <- event:
		default:
		}
	}
}

// broadcastLocked 在已持锁的前提下把事件写入所有客户端发送队列。
func (s *sessionStore) broadcastLocked(rt *sessionRuntime, event serverEvent) {
	for client := range rt.clients {
		select {
		case client.send <- event:
		default:
		}
	}
}

// startClientWriter 持续把发送队列里的事件写入 WebSocket。
func startClientWriter(client *clientConn) {
	for event := range client.send {
		if err := writeClientJSON(client, event); err != nil {
			closeClientConn(client)
			return
		}
	}
}

// closeClientConn 安全关闭客户端发送队列和底层连接。
func closeClientConn(client *clientConn) {
	if client == nil {
		return
	}
	client.closeOnce.Do(func() {
		close(client.send)
		_ = client.conn.Close()
	})
}

// writeClientJSON 为单个客户端安全写入一条 JSON 事件。
func writeClientJSON(client *clientConn, event serverEvent) error {
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	_ = client.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	err := client.conn.WriteJSON(event)
	_ = client.conn.SetWriteDeadline(time.Time{})
	return err
}

// cloneSession 深拷贝会话，避免把内部状态直接暴露给调用方。
func cloneSession(session *Session) *Session {
	if session == nil {
		return nil
	}
	cloned := *session
	cloned.Messages = make([]*Message, 0, len(session.Messages))
	for _, msg := range session.Messages {
		cloned.Messages = append(cloned.Messages, cloneMessage(msg))
	}
	cloned.Events = make([]*Event, 0, len(session.Events))
	for _, event := range session.Events {
		cloned.Events = append(cloned.Events, cloneEvent(event))
	}
	cloned.DraftMessage = cloneMessage(session.DraftMessage)
	return &cloned
}

// cloneMessages 复制消息切片，避免历史恢复时和 provider 内存共享。
func cloneMessages(items []*Message) []*Message {
	out := make([]*Message, 0, len(items))
	for _, item := range items {
		out = append(out, cloneMessage(item))
	}
	return out
}

// cloneEvents 复制事件切片，避免历史恢复时和 provider 内存共享。
func cloneEvents(items []*Event) []*Event {
	out := make([]*Event, 0, len(items))
	for _, item := range items {
		out = append(out, cloneEvent(item))
	}
	return out
}

// cloneMessage 复制单条消息，避免共享同一块内存。
func cloneMessage(message *Message) *Message {
	if message == nil {
		return nil
	}
	cloned := *message
	if len(message.ImageURLs) > 0 {
		cloned.ImageURLs = append([]string(nil), message.ImageURLs...)
	}
	return &cloned
}

// cloneEvent 复制单条事件，避免并发场景下共享同一份内存。
func cloneEvent(event *Event) *Event {
	if event == nil {
		return nil
	}
	cloned := *event
	return &cloned
}

// writeJSON 以统一的 JSON 响应格式写回 HTTP 客户端。
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// mustJSON 把对象编码成 JSON 字符串，适用于已知不会失败的小对象。
func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

// authTokenForPassword 把登录密码转换成固定 token，便于通过 cookie 判断登录态。
func authTokenForPassword(password string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(password)))
	return hex.EncodeToString(sum[:])
}

// isAuthenticated 判断当前请求是否携带了有效登录 cookie。
func (s *sessionStore) isAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie("code_web_auth")
	if err != nil {
		return false
	}
	return strings.TrimSpace(cookie.Value) == s.authToken
}

// withAuth 保护需要登录后才能访问的接口和 WebSocket。
func (s *sessionStore) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" || r.URL.Path == "/api/auth" || r.URL.Path == "/" || r.URL.Path == "/index.html" ||
			r.URL.Path == "/style.css" || r.URL.Path == "/app-config.js" || strings.HasPrefix(r.URL.Path, "/app/") || strings.HasPrefix(r.URL.Path, "/uploads/") {
			next.ServeHTTP(w, r)
			return
		}
		if !s.isAuthenticated(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mergeStreamText 合并 provider 返回的文本，兼容“累计文本”和“增量文本”两种流式格式。
func mergeStreamText(current, incoming string) (next string, delta string) {
	if incoming == "" {
		return current, ""
	}
	if current == "" {
		return incoming, incoming
	}
	if incoming == current {
		return current, ""
	}
	if strings.HasPrefix(incoming, current) {
		return incoming, incoming[len(current):]
	}
	if strings.HasPrefix(current, incoming) {
		return current, ""
	}
	return current + incoming, incoming
}

// listSessions 返回已持久化的本地 web 会话列表。
func (s *sessionStore) listSessions(ctx context.Context) ([]*sessionListItem, error) {
	_ = ctx
	s.mu.RLock()
	items := make([]*sessionListItem, 0, len(s.sessions))
	for _, rt := range s.sessions {
		if rt == nil || rt.session == nil {
			continue
		}
		if !isLocalSessionID(rt.session.ID) {
			continue
		}
		item := &sessionListItem{
			ID:                rt.session.ID,
			Provider:          rt.session.Provider,
			Model:             rt.session.Model,
			Title:             firstMessageSummary(rt.session.Messages),
			Workdir:           rt.session.Workdir,
			ProviderSessionID: rt.session.ProviderSessionID,
			UpdatedAt:         rt.session.UpdatedAt,
			MessageCount:      len(rt.session.Messages),
			Running:           rt.session.IsRunning,
			LastMessage:       lastUserMessage(rt.session.Messages),
			LastEvent:         lastEventSummary(rt.session.Events),
		}
		items = append(items, item)
	}
	s.mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

// firstMessageSummary 返回会话第一条非空消息摘要，优先用于列表标题展示。
func firstMessageSummary(items []*Message) string {
	for _, item := range items {
		if item != nil && strings.TrimSpace(item.Content) != "" {
			return compactEventBody(item.Content)
		}
	}
	return ""
}

// lastUserMessage 返回最近一条用户消息摘要。
func lastUserMessage(items []*Message) string {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i] != nil && items[i].Role == "user" && strings.TrimSpace(items[i].Content) != "" {
			return compactEventBody(items[i].Content)
		}
	}
	return ""
}

// lastEventSummary 返回最近一条事件摘要。
func lastEventSummary(items []*Event) string {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i] == nil {
			continue
		}
		if text := firstNonEmptyString(items[i].Target, items[i].Title, items[i].Body); text != "" {
			return compactEventBody(text)
		}
	}
	return ""
}

// maxTime 返回两个时间中较晚的那个。
func maxTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

// normalizeWorkdir 规范化工作目录，空值时回退到当前项目目录。
func normalizeWorkdir(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		if cwd, err := filepath.Abs("."); err == nil {
			return cwd
		}
		return ""
	}
	if abs, err := filepath.Abs(text); err == nil {
		return abs
	}
	return text
}

// providerByName 根据 provider 名称返回对应实现，空值时回退到 claude。
func (s *sessionStore) providerByName(name string) Provider {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = s.defaultProviderID()
	}
	return s.providers[key]
}

// mustProvider 获取 provider；这里用于已知存在的内置 provider。
func (s *sessionStore) mustProvider(name string) Provider {
	return s.providers[name]
}

// providerBySession 根据会话配置选择真正执行命令的 provider。
func (s *sessionStore) providerBySession(session *Session) Provider {
	if session == nil {
		return s.mustProvider("claude")
	}
	if provider := s.providerByName(session.Provider); provider != nil {
		return provider
	}
	return s.mustProvider("claude")
}

// defaultProviderID 返回当前应用配置中的默认 provider。
func (s *sessionStore) defaultProviderID() string {
	if s == nil || s.appConfig == nil {
		return "claude"
	}
	return s.appConfig.defaultProviderID()
}

// defaultModelForProvider 返回指定 provider 的默认模型。
func (s *sessionStore) defaultModelForProvider(providerID string) string {
	if s != nil && s.appConfig != nil {
		if model := s.appConfig.providerDefaultModel(providerID); model != "" {
			return model
		}
		if models := s.appConfig.providerModels(providerID); len(models) > 0 {
			return models[0]
		}
	}
	if provider := s.providerByName(providerID); provider != nil {
		return provider.DefaultModel()
	}
	return ""
}

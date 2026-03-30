package main

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func (s *sessionStore) handleNewSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	var req newSessionRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid json body", nil)
			return
		}
	}
	workdir, err := validateWorkdir(req.Workdir)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	providerID := strings.TrimSpace(strings.ToLower(req.Provider))
	if providerID == "" {
		providerID = activeProvider.ID()
	}
	if _, ok := availableProviders[providerID]; !ok {
		writeAPIError(w, http.StatusBadRequest, "provider is not available", nil)
		return
	}
	session := s.ensureRuntime("", workdir, providerID).session
	writeAPIJSON(w, map[string]string{"sessionId": session.ID})
}

func (s *sessionStore) handleRestoreSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	var req restoreSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	session, err := s.restoreSession(ctx, req)
	if err != nil {
		appLog.Warn().Err(err).Msg("restore session failed")
		writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	writeAPIJSON(w, map[string]string{"sessionId": session.ID})
}

func (s *sessionStore) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	if authTokenForPassword(req.Password) != s.authToken {
		writeAPIError(w, http.StatusUnauthorized, "invalid password", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    s.authToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})
	writeAPIJSON(w, map[string]bool{"ok": true})
}

func (s *sessionStore) handleAuth(w http.ResponseWriter, r *http.Request) {
	writeAPIJSON(w, map[string]bool{"authenticated": s.isAuthenticated(r)})
}

func (s *sessionStore) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeAPIJSON(w, map[string]bool{"ok": true})
}

func (s *sessionStore) handleAppConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte("window.__APP_CONFIG = " + mustJSObject(map[string]interface{}{
		"version":         strings.TrimSpace(appVersion),
		"assetVersion":    strings.TrimSpace(assetVersion),
		"authGuideSteps":  authGuideSteps,
		"provider":        activeProvider.ID(),
		"providerName":    activeProvider.DisplayName(),
		"appName":         activeProvider.AppName(),
		"requiresAuth":    activeProvider.RequiresAuth(),
		"supportsFast":    supportsFastMode(),
		"supportsCompact": supportsCompact(),
		"providers":       availableProviderList(),
	}) + ";\n"))
}

func (s *sessionStore) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		staticAssetsHandler("static").ServeHTTP(w, r)
		return
	}

	content, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		http.Error(w, "index file not found", http.StatusInternalServerError)
		return
	}

	html := strings.ReplaceAll(string(content), "__ASSET_VERSION__", assetVersion)
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func staticAssetsHandler(_ string) http.Handler {
	fileServer := http.FileServer(http.FS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.URL.Query().Get("v")) != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache, max-age=0")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *sessionStore) handleCodexAuthPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := availableProviders[providerCodex]; !ok {
		http.NotFound(w, r)
		return
	}
	content, err := fs.ReadFile(staticFS, "codex-auth.html")
	if err != nil {
		http.Error(w, "codex auth page not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(content)
}

func (s *sessionStore) handleCodexAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	if _, ok := availableProviders[providerCodex]; !ok {
		writeAPIJSON(w, codexAuthStatusResponse{LoggedIn: true})
		return
	}
	writeAPIJSON(w, s.auth.Status())
}

func (s *sessionStore) handleCodexAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	if _, ok := availableProviders[providerCodex]; !ok {
		writeAPIError(w, http.StatusNotFound, "current provider does not use codex auth", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeAPIJSON(w, s.auth.EnsureStarted(ctx, codexAuthRestart(r.URL.Query().Get("restart"))))
}

func (s *sessionStore) handleCodexAuthComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	if _, ok := availableProviders[providerCodex]; !ok {
		writeAPIError(w, http.StatusNotFound, "current provider does not use codex auth", nil)
		return
	}
	var req codexAuthCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	s.auth.mu.Lock()
	current := s.auth.session
	s.auth.mu.Unlock()
	if current == nil || strings.TrimSpace(req.SessionID) == "" || req.SessionID != current.ID {
		appLog.Warn().Bool("current_exists", current != nil).Str("incoming_session_id", strings.TrimSpace(req.SessionID)).Msg("auth complete rejected: session mismatch")
		writeAPIError(w, http.StatusBadRequest, authStatusMessage("state mismatch"), codexAuthStatusResponse{
			LoggedIn: false,
			Session: &codexAuthSession{
				ID:     strings.TrimSpace(req.SessionID),
				Status: "failed",
				Error:  authStatusMessage("state mismatch"),
			},
		})
		return
	}
	if parsed, err := url.Parse(strings.TrimSpace(req.CallbackURL)); err != nil || strings.TrimSpace(parsed.Query().Get("state")) == "" || strings.TrimSpace(parsed.Query().Get("state")) != current.State {
		appLog.Warn().Str("session", current.ID).Msg("auth complete rejected: state mismatch")
		writeAPIError(w, http.StatusBadRequest, authStatusMessage("state mismatch"), codexAuthStatusResponse{
			LoggedIn: false,
			Session: &codexAuthSession{
				ID:     current.ID,
				Status: "failed",
				Error:  authStatusMessage("state mismatch"),
			},
		})
		return
	}
	if err := completeCodexAuth(ctx, req.CallbackURL); err != nil {
		appLog.Error().Str("session", current.ID).Err(err).Msg("auth complete failed")
		s.auth.mu.Lock()
		if s.auth.session != nil {
			s.auth.session.Status = "failed"
			s.auth.session.Error = authStatusMessage(err.Error())
		}
		s.auth.mu.Unlock()
		writeAPIError(w, http.StatusBadRequest, authStatusMessage(err.Error()), s.auth.Status())
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if loggedIn, _ := s.auth.loginStatus(true); loggedIn {
			if s.app != nil {
				s.resetSessionThreads()
				restartErr := s.app.Restart(ctx, "Codex 登录账号已切换，当前任务已中断")
				if restartErr != nil {
					appLog.Error().Str("session", current.ID).Err(restartErr).Msg("auth complete restart failed")
					writeAPIError(w, http.StatusInternalServerError, "codex app-server restart failed", nil)
					return
				}
			}
			appLog.Info().Str("session", current.ID).Msg("auth complete confirmed")
			writeAPIJSON(w, s.auth.Status())
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	appLog.Warn().Str("session", current.ID).Msg("auth complete pending after callback")
	writeAPIJSON(w, s.auth.Status())
}

func (s *sessionStore) handleCodexAuthCallback(w http.ResponseWriter, r *http.Request) {
	if _, ok := availableProviders[providerCodex]; !ok {
		http.NotFound(w, r)
		return
	}
	target, err := url.Parse(codexAuthProxyTarget())
	if err != nil {
		http.Error(w, "invalid callback target", http.StatusInternalServerError)
		return
	}
	proxyURL := *target
	proxyURL.Path = "/auth/callback"
	proxyURL.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, proxyURL.String(), r.Body)
	if err != nil {
		http.Error(w, "build callback request failed", http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Host = target.Host

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "codex login callback is not ready", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *sessionStore) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/login" || path == "/api/auth" || path == "/api/logout" || path == "/" || path == "/index.html" || path == "/app.js" || path == "/style.css" || path == "/app-config.js" {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(path, "/api/") && path != "/ws" && !strings.HasPrefix(path, "/uploads/") {
			next.ServeHTTP(w, r)
			return
		}
		if !s.isAuthenticated(r) {
			if path == "/ws" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *sessionStore) isAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}
	return cookie.Value != "" && cookie.Value == s.authToken
}

func (s *sessionStore) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var hello clientEvent
	if err := conn.ReadJSON(&hello); err != nil {
		return
	}
	client := newClientConn(conn)
	if hello.Type != "hello" {
		_ = writeClientJSON(client, serverEvent{Type: "error", Error: "first message must be hello"})
		return
	}

	rt, client := s.attachClient(hello.SessionID, conn)
	defer s.detachClient(rt.session.ID, client)

	if err := writeClientJSON(client, serverEvent{
		Type:    "snapshot",
		Session: s.cloneSession(rt.session.ID),
		Running: rt.session.ActiveTaskID != "",
		TaskID:  rt.session.ActiveTaskID,
		Meta:    func() *appMeta { meta := s.metaForSession(rt.session.ID); return &meta }(),
	}); err != nil {
		return
	}

	for {
		var event clientEvent
		if err := conn.ReadJSON(&event); err != nil {
			break
		}

		switch event.Type {
		case "send":
			if err := s.enqueuePrompt(rt.session.ID, strings.TrimSpace(event.Content), event.ImageIDs); err != nil {
				_ = writeClientJSON(client, serverEvent{Type: "error", Error: err.Error()})
			}
		case "ping":
			if err := writeClientJSON(client, serverEvent{Type: "pong"}); err != nil {
				return
			}
		default:
			_ = writeClientJSON(client, serverEvent{Type: "error", Error: "unsupported event"})
		}
	}
}

func (s *sessionStore) attachClient(sessionID string, conn *websocket.Conn) (*sessionRuntime, *clientConn) {
	rt := s.ensureRuntime(sessionID, "", "")
	client := newClientConn(conn)
	startClientWriter(client)

	s.mu.Lock()
	rt.clients[client] = struct{}{}
	s.mu.Unlock()

	return rt, client
}

func (s *sessionStore) detachClient(sessionID string, target *clientConn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	for client := range rt.clients {
		if client == target {
			delete(rt.clients, client)
			closeClientConn(client)
			return
		}
	}
}

func (s *sessionStore) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid multipart form", nil)
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("sessionId"))
	content := strings.TrimSpace(r.FormValue("content"))
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "missing session id", nil)
		return
	}

	imageIDs, err := s.saveMultipartImages(r.MultipartForm.File["images"])
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	if err := s.enqueuePrompt(sessionID, content, imageIDs); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	writeAPIJSON(w, map[string]bool{"ok": true})
}

func (s *sessionStore) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	if sessionID != "" {
		s.reconcileSessionTask(sessionID)
	}
	resp := statusResponse{
		RestoreRef:     nil,
		Provider:       activeProvider.ID(),
		Model:          defaultModelForProviderID(activeProvider.ID()),
		Cwd:            defaultWorkdir,
		SessionID:      sessionID,
		Transport:      "connected",
		Task:           "idle",
		ApprovalPolicy: s.currentApprovalPolicy(),
		ServiceTier:    s.currentServiceTier(),
		FastMode:       s.currentFastMode(),
	}

	if sessionID != "" {
		session := s.cloneSession(sessionID)
		if session != nil {
			resp.RestoreRef = sessionRestoreRef(session)
			resp.Provider = firstNonEmpty(strings.TrimSpace(session.Provider), activeProvider.ID())
			resp.Model = firstNonEmpty(strings.TrimSpace(session.Model), defaultModelForProviderID(resp.Provider))
			resp.SessionID = session.ID
			resp.Cwd = normalizeWorkdir(session.Workdir)
			resp.CodexThreadID = strings.TrimSpace(session.CodexThreadID)
			resp.ProviderSessionID = strings.TrimSpace(session.ProviderSessionID)
			if session.ActiveTaskID != "" {
				resp.Task = "running"
			}
		}
	}

	writeAPIJSON(w, resp)
}

func (s *sessionStore) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	providerID := activeProvider.ID()
	currentModel := defaultModelForProviderID(providerID)
	if sessionID != "" {
		providerID = s.sessionProvider(sessionID)
		currentModel = s.sessionModel(sessionID)
	}
	resp := modelsResponse{Provider: providerID, Current: currentModel, Items: []modelInfo{}}
	if items, err := providerForID(providerID).ListModels(r.Context(), s, sessionID); err == nil {
		resp.Items = items
	}
	if len(resp.Items) == 0 {
		resp.Items = defaultModelsForProvider(providerID)
	}
	writeAPIJSON(w, resp)
}

func (s *sessionStore) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	items, err := listInstalledSkills()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), nil)
		return
	}
	writeAPIJSON(w, skillsResponse{Items: items})
}

func (s *sessionStore) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	writeAPIJSON(w, sessionsResponse{Items: s.listSessions()})
}

func (s *sessionStore) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}

	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Command = strings.TrimSpace(req.Command)
	req.Args = strings.TrimSpace(req.Args)
	if req.SessionID == "" || req.Command == "" {
		writeAPIError(w, http.StatusBadRequest, "missing sessionId or command", nil)
		return
	}

	switch req.Command {
	case "/review":
		if err := s.enqueueReview(req.SessionID, req.Args); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeAPIJSON(w, map[string]bool{"ok": true})
	case "/model":
		model, err := s.setModel(req.SessionID, req.Args)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeAPIJSON(w, map[string]interface{}{"ok": true, "model": model})
	case "/approvals":
		mode, err := s.setApprovalPolicy(req.Args)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeAPIJSON(w, map[string]interface{}{"ok": true, "approvalPolicy": mode})
	case "/fast":
		mode, serviceTier, err := s.setFastMode(req.SessionID, req.Args)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeAPIJSON(w, map[string]interface{}{"ok": true, "fastMode": mode, "serviceTier": serviceTier})
	case "/compact":
		compacted, err := s.compactSession(req.SessionID)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeAPIJSON(w, map[string]interface{}{"ok": true, "compacted": compacted})
	case "/stop":
		stopped, err := s.stopActiveTask(req.SessionID)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeAPIJSON(w, map[string]interface{}{"ok": true, "stopped": stopped})
	case "/delete":
		targetID := req.SessionID
		if req.Args != "" {
			targetID = req.Args
		}
		deleted, err := s.deleteSession(targetID)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeAPIJSON(w, map[string]interface{}{"ok": true, "deleted": deleted, "sessionId": targetID})
	default:
		writeAPIError(w, http.StatusBadRequest, "unsupported command", nil)
	}
}

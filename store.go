package main

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *sessionStore) ensureSession(sessionID string) *Session {
	return s.ensureSessionWithWorkdir(sessionID, "")
}

func (s *sessionStore) ensureSessionWithWorkdir(sessionID, workdir string) *Session {
	return s.ensureRuntime(sessionID, workdir, "").session
}

func (s *sessionStore) restoreSession(ctx context.Context, req restoreSessionRequest) (*Session, error) {
	ref := normalizeRestoreRef(req.RestoreRef)
	providerID := strings.TrimSpace(strings.ToLower(req.Provider))
	if ref != nil {
		providerID = ref.Provider
	}
	if providerID == "" {
		providerID = activeProvider.ID()
	}
	if _, ok := availableProviders[providerID]; !ok {
		return nil, errors.New("provider is not available")
	}

	workdir := normalizeWorkdir(strings.TrimSpace(req.Workdir))
	if ref != nil && strings.TrimSpace(ref.Workdir) != "" {
		workdir = normalizeWorkdir(ref.Workdir)
	}
	if workdir == "" || workdir == "." {
		workdir = defaultWorkdir
	}

	rt := s.ensureRuntime("", workdir, providerID)
	sessionID := rt.session.ID

	s.mu.Lock()
	rt.session.Provider = providerID
	if model := firstNonEmpty(
		func() string {
			if ref == nil {
				return ""
			}
			return strings.TrimSpace(ref.Model)
		}(),
		strings.TrimSpace(req.Model),
	); model != "" {
		rt.session.Model = model
	}
	rt.session.Workdir = workdir
	rt.session.CodexThreadID = firstNonEmpty(
		func() string {
			if ref == nil {
				return ""
			}
			return strings.TrimSpace(ref.CodexThreadID)
		}(),
		strings.TrimSpace(req.CodexThreadID),
	)
	rt.session.ProviderSessionID = firstNonEmpty(
		func() string {
			if ref == nil {
				return ""
			}
			return strings.TrimSpace(ref.ProviderSessionID)
		}(),
		strings.TrimSpace(req.ProviderSessionID),
	)
	rt.session.Messages = make([]Message, 0, 32)
	rt.session.Events = make([]EventLog, 0, 64)
	rt.session.DraftMessage = nil
	rt.session.ActiveTaskID = ""
	rt.session.ActiveTurnID = ""
	rt.stopRequested = false
	rt.cancelTask = nil
	rt.session.UpdatedAt = time.Now()
	s.mu.Unlock()

	switch providerID {
	case providerCodex:
		if strings.TrimSpace(rt.session.CodexThreadID) == "" {
			return nil, errors.New("missing codex thread id")
		}
		if s.app == nil {
			return nil, errors.New("codex app-server is not available")
		}
		if err := s.hydrateCodexSession(ctx, sessionID, strings.TrimSpace(rt.session.CodexThreadID)); err != nil {
			return nil, err
		}
	case providerClaude:
		if strings.TrimSpace(rt.session.ProviderSessionID) == "" {
			return nil, errors.New("missing claude session id")
		}
		if len(req.Messages) > 0 || len(req.Events) > 0 || req.DraftMessage != nil {
			s.replaceTimeline(sessionID, req.Messages, req.Events)
			s.mu.Lock()
			if existing, ok := s.sessions[sessionID]; ok {
				existing.session.DraftMessage = cloneMessagePtr(req.DraftMessage)
				existing.session.UpdatedAt = time.Now()
			}
			s.mu.Unlock()
		} else {
			s.appendMessage(sessionID, "system", "已恢复 Claude 远端会话，后续消息会继续复用该会话上下文。")
		}
	}

	return s.cloneSession(sessionID), nil
}

func (s *sessionStore) ensureRuntime(sessionID, workdir, providerID string) *sessionRuntime {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sessionID != "" {
		if rt, ok := s.sessions[sessionID]; ok {
			if rt.session.Workdir == "" {
				rt.session.Workdir = normalizeWorkdir(workdir)
			}
			return rt
		}
	}

	now := time.Now()
	providerID = strings.TrimSpace(strings.ToLower(providerID))
	if providerID == "" {
		providerID = activeProvider.ID()
	}
	session := &Session{
		ID:        uuid.NewString(),
		Provider:  providerID,
		Model:     defaultModelForProviderID(providerID),
		Workdir:   normalizeWorkdir(workdir),
		Messages:  make([]Message, 0, 16),
		Events:    make([]EventLog, 0, 32),
		UpdatedAt: now,
	}
	rt := &sessionRuntime{
		session: session,
		clients: make(map[*clientConn]struct{}),
	}
	s.sessions[session.ID] = rt
	return rt
}

func (s *sessionStore) currentModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.meta.Model)
}

func (s *sessionStore) sessionProvider(sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		return activeProvider.ID()
	}
	return firstNonEmpty(strings.TrimSpace(rt.session.Provider), providerCodex)
}

func (s *sessionStore) sessionModel(sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		return defaultModelForProviderID(activeProvider.ID())
	}
	providerID := firstNonEmpty(strings.TrimSpace(rt.session.Provider), providerCodex)
	model := strings.TrimSpace(rt.session.Model)
	if model != "" {
		return model
	}
	return defaultModelForProviderID(providerID)
}

func (s *sessionStore) currentApprovalPolicy() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.meta.ApprovalPolicy)
}

func (s *sessionStore) currentServiceTier() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.meta.ServiceTier)
}

func (s *sessionStore) currentFastMode() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.meta.FastMode
}

func (s *sessionStore) setModel(sessionID, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return s.sessionModel(sessionID), nil
	}
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return "", errors.New("session not found")
	}
	rt.session.Model = value
	rt.session.UpdatedAt = time.Now()
	s.mu.Unlock()
	providerForID(s.sessionProvider(sessionID)).InvalidateSession(s, sessionID)
	s.broadcastMeta(sessionID)
	return value, nil
}

func (s *sessionStore) setApprovalPolicy(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return s.currentApprovalPolicy(), nil
	}
	if value != "never" {
		return "", errors.New("web mode currently only supports approvals=never")
	}
	s.mu.Lock()
	s.meta.ApprovalPolicy = value
	s.mu.Unlock()
	s.broadcastMetaToProviders(providerCodex)
	s.broadcastMetaToProviders(providerClaude)
	return value, nil
}

func (s *sessionStore) setFastMode(sessionID, value string) (bool, string, error) {
	return providerForID(s.sessionProvider(sessionID)).SetFastMode(s, sessionID, value)
}

func (s *sessionStore) writeServiceTier(value string) error {
	if s.app == nil {
		return errors.New("codex app-server is not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return s.app.WriteConfigValue(ctx, "service_tier", value)
}

func (s *sessionStore) clearServiceTier() error {
	if s.app == nil {
		return errors.New("codex app-server is not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return s.app.ClearConfigValue(ctx, "service_tier")
}

func (s *sessionStore) resetSessionThreads() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, rt := range s.sessions {
		if rt.session.Provider != providerCodex {
			continue
		}
		if rt.session.CodexThreadID == "" && rt.session.ProviderSessionID == "" && rt.session.ActiveTurnID == "" {
			continue
		}
		rt.session.CodexThreadID = ""
		rt.session.ProviderSessionID = ""
		rt.session.ActiveTurnID = ""
		rt.session.UpdatedAt = time.Now()
	}
}

func (s *sessionStore) stopActiveTask(sessionID string) (bool, error) {
	session := s.cloneSession(sessionID)
	if session == nil {
		return false, errors.New("session not found")
	}
	if strings.TrimSpace(session.ActiveTaskID) == "" {
		return false, nil
	}
	return providerForID(session.Provider).StopActiveTask(s, sessionID)
}

func (s *sessionStore) compactSession(sessionID string) (bool, error) {
	return providerForID(s.sessionProvider(sessionID)).CompactSession(s, sessionID)
}

func (s *sessionStore) sessionWorkdir(sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return defaultWorkdir
	}
	return normalizeWorkdir(rt.session.Workdir)
}

func (s *sessionStore) metaForSession(sessionID string) appMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		return s.meta
	}
	meta := s.meta
	meta.Provider = firstNonEmpty(strings.TrimSpace(rt.session.Provider), providerCodex)
	meta.Model = firstNonEmpty(strings.TrimSpace(rt.session.Model), defaultModelForProviderID(meta.Provider))
	meta.Cwd = normalizeWorkdir(rt.session.Workdir)
	if !supportsFastModeFor(meta.Provider) {
		meta.ServiceTier = ""
		meta.FastMode = false
	}
	return meta
}

func (s *sessionStore) broadcastMeta(sessionID string) {
	s.mu.RLock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.RUnlock()
		return
	}
	clients := cloneClients(rt.clients)
	s.mu.RUnlock()
	meta := s.metaForSession(sessionID)
	broadcastJSON(clients, serverEvent{Type: "meta_update", Meta: &meta})
}

func (s *sessionStore) broadcastMetaToProviders(providerID string) {
	s.mu.RLock()
	sessionIDs := make([]string, 0, len(s.sessions))
	for sessionID, rt := range s.sessions {
		if firstNonEmpty(strings.TrimSpace(rt.session.Provider), providerCodex) == providerID {
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	s.mu.RUnlock()
	for _, sessionID := range sessionIDs {
		s.broadcastMeta(sessionID)
	}
}

func (s *sessionStore) enqueuePrompt(sessionID, content string, imageIDs []string) error {
	if content == "" && len(imageIDs) == 0 {
		return errors.New("message is empty")
	}
	if s.sessionProvider(sessionID) == providerCodex && s.app == nil {
		return errors.New("codex app-server is not available")
	}

	imageURLs, imagePaths, err := resolveImageFiles(imageIDs)
	if err != nil {
		return err
	}

	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return errors.New("session not found")
	}
	if rt.session.ActiveTaskID != "" {
		s.mu.Unlock()
		return errors.New("a task is already running in this session")
	}

	userMsg := Message{
		ID:        uuid.NewString(),
		Role:      "user",
		Content:   content,
		ImageURLs: imageURLs,
		CreatedAt: time.Now(),
	}
	rt.session.Messages = append(rt.session.Messages, userMsg)
	rt.session.UpdatedAt = time.Now()
	taskID := uuid.NewString()
	rt.session.ActiveTaskID = taskID
	clients := cloneClients(rt.clients)
	s.mu.Unlock()

	broadcastJSON(clients, serverEvent{Type: "message", Message: &userMsg})
	broadcastJSON(clients, serverEvent{Type: "task_status", TaskID: taskID, Running: true})

	providerForID(rt.session.Provider).RunPrompt(s, sessionID, taskID, content, imagePaths)
	return nil
}

func (s *sessionStore) enqueueReview(sessionID, args string) error {
	var taskID string
	var clients map[*clientConn]struct{}
	commandText := "/review"
	if args != "" {
		commandText += " " + args
	}

	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return errors.New("session not found")
	}
	if rt.session.ActiveTaskID != "" {
		s.mu.Unlock()
		return errors.New("a task is already running in this session")
	}

	userMsg := Message{
		ID:        uuid.NewString(),
		Role:      "user",
		Content:   commandText,
		CreatedAt: time.Now(),
	}
	rt.session.Messages = append(rt.session.Messages, userMsg)
	rt.session.UpdatedAt = time.Now()
	taskID = uuid.NewString()
	rt.session.ActiveTaskID = taskID
	clients = cloneClients(rt.clients)
	s.mu.Unlock()

	broadcastJSON(clients, serverEvent{Type: "message", Message: &userMsg})
	broadcastJSON(clients, serverEvent{Type: "task_status", TaskID: taskID, Running: true})
	providerForID(s.sessionProvider(sessionID)).RunReview(s, sessionID, taskID, args)
	return nil
}

func (s *sessionStore) runAppServerTask(sessionID, taskID, prompt string, imagePaths []string) {
	waited := s.acquireTaskSlot(sessionID)
	defer s.releaseTaskSlot()

	if waited {
		s.appendEvent(sessionID, "status", "task dequeued", "")
	}

	ctx, cancel := context.WithTimeout(context.Background(), appServerRPCTimeout)
	defer cancel()

	threadID, err := s.app.StartTurn(ctx, sessionID, taskID, s.sessionWorkdir(sessionID), prompt, imagePaths)
	if err != nil {
		s.finishTaskWithError(sessionID, taskID, err)
		return
	}

	s.monitorAppServerTurn(sessionID, taskID, threadID, prompt, time.Now())
}

func (s *sessionStore) runReviewTask(sessionID, taskID, args string) {
	waited := s.acquireTaskSlot(sessionID)
	defer s.releaseTaskSlot()

	if waited {
		s.appendEvent(sessionID, "status", "task dequeued", "")
	}

	s.appendEvent(sessionID, "status", "review started", "")
	if s.sessionProvider(sessionID) != providerCodex {
		s.finishTaskWithError(sessionID, taskID, errors.New("review is only available with codex review"))
		return
	}
	cmdArgs := []string{"review", "--uncommitted"}
	if model := s.sessionModel(sessionID); model != "" {
		cmdArgs = append(cmdArgs, "-m", model)
	}
	if args != "" {
		cmdArgs = append(cmdArgs, args)
	}

	cmd := exec.CommandContext(context.Background(), "codex", cmdArgs...)
	cmd.Dir = s.sessionWorkdir(sessionID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		s.finishTaskWithError(sessionID, taskID, fmt.Errorf("review failed: %s", msg))
		return
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		result = "No review output."
	}
	s.appendMessage(sessionID, "assistant", result)
	s.finishTaskOK(sessionID, taskID)
}

func (s *sessionStore) acquireTaskSlot(sessionID string) bool {
	select {
	case s.taskSlots <- struct{}{}:
		return false
	default:
		s.appendEvent(sessionID, "status", "task queued", fmt.Sprintf("waiting for an available slot (%d max)", s.maxConcurrent))
		s.taskSlots <- struct{}{}
		return true
	}
}

func (s *sessionStore) releaseTaskSlot() {
	select {
	case <-s.taskSlots:
	default:
	}
}

func (s *sessionStore) monitorAppServerTurn(sessionID, taskID, threadID, prompt string, startedAt time.Time) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}

	timer := time.NewTimer(appServerTurnTimeout)
	defer timer.Stop()

	attempt := 1
	for {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(appServerTurnTimeout)

		select {
		case <-timer.C:
		}

		if !s.isTaskActive(sessionID, taskID) {
			return
		}

		s.app.markSuspect(fmt.Sprintf("turn timeout after %s", appServerTurnTimeout))
		s.appendEvent(sessionID, "status", "任务长时间无响应，正在检查远端线程", fmt.Sprintf("本次 turn 已超过 %s 未收到完成信号，正在通过独立 CLI 读取远端线程历史确认结果（第 %d 次）", appServerTurnTimeout, attempt))

		checkCtx, cancel := context.WithTimeout(context.Background(), appServerThreadReadTTL)
		thread, err := s.app.ReadThreadViaIsolatedCLI(checkCtx, threadID)
		cancel()
		if err != nil {
			s.appendEvent(sessionID, "status", "读取远端线程历史失败", err.Error())
			attempt++
			continue
		}

		outcome := detectRecoveredTurnOutcome(thread, s.cloneSession(sessionID), prompt, startedAt)
		if !outcome.Done {
			attempt++
			continue
		}

		s.applyRecoveredCodexThread(sessionID, thread)
		s.appendEvent(sessionID, "status", "已从远端线程历史恢复任务结果", outcome.Summary)
		s.finishRecoveredTask(sessionID, taskID, outcome.Err)
		return
	}
}

func (s *sessionStore) setTaskCancel(sessionID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rt, ok := s.sessions[sessionID]; ok {
		rt.cancelTask = cancel
	}
}

func (s *sessionStore) clearTaskCancel(sessionID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rt, ok := s.sessions[sessionID]; ok && rt.cancelTask != nil {
		rt.cancelTask = nil
	}
}

func (s *sessionStore) cancelTask(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, ok := s.sessions[sessionID]
	if !ok || rt.cancelTask == nil {
		return false
	}
	rt.cancelTask()
	return true
}

func (s *sessionStore) updateProviderSessionID(sessionID, providerSessionID string) {
	providerSessionID = strings.TrimSpace(providerSessionID)
	if providerSessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	if rt.session.ProviderSessionID == providerSessionID {
		return
	}
	rt.session.ProviderSessionID = providerSessionID
	rt.session.UpdatedAt = time.Now()
}

func (s *sessionStore) appendAssistantDelta(sessionID, itemID, delta string) {
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}

	draft := rt.session.DraftMessage
	if draft == nil || draft.ID != itemID {
		draft = &Message{
			ID:        firstNonEmpty(itemID, uuid.NewString()),
			Role:      "assistant",
			CreatedAt: time.Now(),
		}
	}
	draft.Content += delta
	rt.session.DraftMessage = draft
	rt.session.UpdatedAt = time.Now()
	clients := cloneClients(rt.clients)
	copyDraft := *draft
	s.mu.Unlock()

	broadcastJSON(clients, serverEvent{Type: "message_delta", Message: &copyDraft})
}

func (s *sessionStore) completeAssistantMessage(sessionID, itemID, text string) {
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}

	var msg Message
	if rt.session.DraftMessage != nil && (itemID == "" || rt.session.DraftMessage.ID == itemID) {
		msg = *rt.session.DraftMessage
	} else {
		msg = Message{
			ID:        firstNonEmpty(itemID, uuid.NewString()),
			Role:      "assistant",
			CreatedAt: time.Now(),
		}
	}
	if strings.TrimSpace(text) != "" {
		msg.Content = text
	}
	if strings.TrimSpace(msg.Content) == "" {
		rt.session.DraftMessage = nil
		rt.session.UpdatedAt = time.Now()
		s.mu.Unlock()
		return
	}

	updated := false
	if strings.TrimSpace(msg.ID) != "" {
		for i := len(rt.session.Messages) - 1; i >= 0; i-- {
			existing := &rt.session.Messages[i]
			if existing.Role != "assistant" || strings.TrimSpace(existing.ID) != strings.TrimSpace(msg.ID) {
				continue
			}
			existing.Content = msg.Content
			updated = true
			msg = *existing
			break
		}
	}
	if !updated {
		rt.session.Messages = append(rt.session.Messages, msg)
	}
	rt.session.DraftMessage = nil
	rt.session.UpdatedAt = time.Now()
	clients := cloneClients(rt.clients)
	s.mu.Unlock()

	broadcastJSON(clients, serverEvent{Type: "message_final", Message: &msg})
}

func (s *sessionStore) flushDraftMessage(sessionID string) {
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok || rt.session.DraftMessage == nil || strings.TrimSpace(rt.session.DraftMessage.Content) == "" {
		s.mu.Unlock()
		return
	}

	msg := *rt.session.DraftMessage
	rt.session.Messages = append(rt.session.Messages, msg)
	rt.session.DraftMessage = nil
	rt.session.UpdatedAt = time.Now()
	clients := cloneClients(rt.clients)
	s.mu.Unlock()

	broadcastJSON(clients, serverEvent{Type: "message_final", Message: &msg})
}

func (s *sessionStore) finishTaskOK(sessionID, taskID string) {
	s.flushDraftMessage(sessionID)

	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if ok && rt.session.ActiveTaskID == taskID {
		rt.session.ActiveTaskID = ""
		rt.session.ActiveTurnID = ""
		rt.stopRequested = false
		rt.cancelTask = nil
		rt.session.UpdatedAt = time.Now()
	}
	clients := map[*clientConn]struct{}{}
	if ok {
		clients = cloneClients(rt.clients)
	}
	s.mu.Unlock()

	broadcastJSON(clients, serverEvent{Type: "task_status", TaskID: taskID, Running: false})
}

func (s *sessionStore) finishActiveTaskOK(sessionID string) {
	taskID := s.activeTaskID(sessionID)
	if taskID == "" {
		return
	}
	s.finishTaskOK(sessionID, taskID)
}

func (s *sessionStore) finishTaskWithError(sessionID, taskID string, err error) {
	s.flushDraftMessage(sessionID)
	s.appendMessage(sessionID, "system", "任务执行失败："+err.Error())

	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if ok && rt.session.ActiveTaskID == taskID {
		rt.session.ActiveTaskID = ""
		rt.session.ActiveTurnID = ""
		rt.stopRequested = false
		rt.cancelTask = nil
		rt.session.UpdatedAt = time.Now()
	}
	clients := map[*clientConn]struct{}{}
	if ok {
		clients = cloneClients(rt.clients)
	}
	s.mu.Unlock()

	broadcastJSON(clients, serverEvent{Type: "error", Error: err.Error()})
	broadcastJSON(clients, serverEvent{Type: "task_status", TaskID: taskID, Running: false})
}

func (s *sessionStore) finishRecoveredTask(sessionID, taskID string, err error) {
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if ok {
		rt.stopRequested = false
		rt.cancelTask = nil
		rt.session.ActiveTaskID = ""
		rt.session.ActiveTurnID = ""
		rt.session.UpdatedAt = time.Now()
	}
	clients := map[*clientConn]struct{}{}
	if ok {
		clients = cloneClients(rt.clients)
	}
	s.mu.Unlock()

	if err != nil {
		broadcastJSON(clients, serverEvent{Type: "error", Error: err.Error()})
	}
	broadcastJSON(clients, serverEvent{Type: "task_status", TaskID: taskID, Running: false})
}

func (s *sessionStore) finishActiveTaskWithError(sessionID string, err error) {
	taskID := s.activeTaskID(sessionID)
	if taskID == "" {
		return
	}
	s.finishTaskWithError(sessionID, taskID, err)
}

func (s *sessionStore) failAllActiveTasks(message string) {
	s.mu.RLock()
	sessionIDs := make([]string, 0, len(s.sessions))
	for sessionID, rt := range s.sessions {
		if rt.session.ActiveTaskID != "" {
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	s.mu.RUnlock()

	for _, sessionID := range sessionIDs {
		s.finishActiveTaskWithError(sessionID, errors.New(message))
	}
}

func (s *sessionStore) activeTaskID(sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return ""
	}
	return rt.session.ActiveTaskID
}

func (s *sessionStore) isTaskActive(sessionID, taskID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	return strings.TrimSpace(rt.session.ActiveTaskID) == strings.TrimSpace(taskID)
}

func (s *sessionStore) reconcileSessionTask(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}

	var taskID string
	var clients map[*clientConn]struct{}
	var providerID string

	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok || strings.TrimSpace(rt.session.ActiveTaskID) == "" {
		s.mu.Unlock()
		return false
	}
	if strings.TrimSpace(rt.session.ActiveTurnID) != "" {
		s.mu.Unlock()
		return false
	}
	if time.Since(rt.session.UpdatedAt) < staleTaskIdle {
		s.mu.Unlock()
		return false
	}
	providerID = firstNonEmpty(strings.TrimSpace(rt.session.Provider), providerCodex)
	if providerID == providerCodex && s.app != nil && s.app.hasKnownActiveTurn(rt.session) {
		s.mu.Unlock()
		return false
	}

	taskID = rt.session.ActiveTaskID
	rt.session.ActiveTaskID = ""
	rt.stopRequested = false
	rt.cancelTask = nil
	rt.session.UpdatedAt = time.Now()
	clients = cloneClients(rt.clients)
	s.mu.Unlock()

	s.appendMessage(sessionID, "system", "检测到卡住的任务状态，已自动恢复为空闲。")
	broadcastJSON(clients, serverEvent{Type: "task_status", TaskID: taskID, Running: false})
	return true
}

func (s *sessionStore) appendMessage(sessionID, role, content string) {
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}

	msg := Message{
		ID:        uuid.NewString(),
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
	}
	rt.session.Messages = append(rt.session.Messages, msg)
	rt.session.UpdatedAt = time.Now()
	clients := cloneClients(rt.clients)
	s.mu.Unlock()

	broadcastJSON(clients, serverEvent{Type: "message", Message: &msg})
}

func (s *sessionStore) replaceTimeline(sessionID string, messages []Message, events []EventLog) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	rt.session.Messages = append([]Message(nil), messages...)
	rt.session.Events = append([]EventLog(nil), events...)
	rt.session.DraftMessage = nil
	rt.session.ActiveTaskID = ""
	rt.session.ActiveTurnID = ""
	rt.stopRequested = false
	rt.cancelTask = nil
	rt.session.UpdatedAt = time.Now()
}

func (s *sessionStore) broadcastSessionSnapshot(sessionID string) {
	s.mu.RLock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.RUnlock()
		return
	}
	clients := cloneClients(rt.clients)
	s.mu.RUnlock()

	meta := s.metaForSession(sessionID)
	broadcastJSON(clients, serverEvent{
		Type:    "snapshot",
		Session: s.cloneSession(sessionID),
		Running: false,
		Meta:    &meta,
	})
}

func (s *sessionStore) applyRecoveredCodexThread(sessionID string, thread map[string]interface{}) {
	threadID := strings.TrimSpace(firstNonEmpty(
		lookupNestedString(thread, "id"),
		lookupNestedString(thread, "thread.id"),
	))
	messages, events := buildCodexHistoryTimeline(thread)
	s.replaceTimeline(sessionID, messages, events)
	if threadID != "" {
		s.updateThreadID(sessionID, threadID)
	}
	if workdir := strings.TrimSpace(lookupNestedString(thread, "cwd")); workdir != "" {
		s.mu.Lock()
		if rt, ok := s.sessions[sessionID]; ok {
			rt.session.Workdir = normalizeWorkdir(workdir)
			rt.session.UpdatedAt = time.Now()
		}
		s.mu.Unlock()
	}
	if s.app != nil && threadID != "" {
		s.app.recordThreadSession(sessionID, threadID, false)
	}
	s.broadcastSessionSnapshot(sessionID)
}

func cloneMessagePtr(msg *Message) *Message {
	if msg == nil {
		return nil
	}
	copyMsg := *msg
	if len(msg.ImageURLs) > 0 {
		copyMsg.ImageURLs = append([]string(nil), msg.ImageURLs...)
	}
	return &copyMsg
}

func (s *sessionStore) hydrateCodexSession(ctx context.Context, sessionID, threadID string) error {
	thread, err := s.app.ReadThread(ctx, threadID)
	if err != nil {
		return err
	}
	s.applyRecoveredCodexThread(sessionID, thread)
	return nil
}

type recoveredTurnOutcome struct {
	Done    bool
	Summary string
	Err     error
}

func detectRecoveredTurnOutcome(thread map[string]interface{}, session *Session, prompt string, startedAt time.Time) recoveredTurnOutcome {
	target := selectRecoveredTurn(thread, session, prompt, startedAt)
	if target == nil {
		return recoveredTurnOutcome{}
	}

	status := strings.ToLower(strings.TrimSpace(itemField(target, "status")))
	switch status {
	case "completed", "complete", "succeeded", "success":
		return recoveredTurnOutcome{
			Done:    true,
			Summary: "远端线程显示该任务已经完成，已按远端结果恢复当前会话。",
		}
	case "failed", "error", "interrupted", "cancelled", "canceled":
		message := firstNonEmpty(
			lookupNestedString(target, "error.message"),
			itemField(target, "error.message"),
			"任务执行失败",
		)
		return recoveredTurnOutcome{
			Done:    true,
			Summary: "远端线程显示该任务已经失败，已同步失败状态和错误信息。",
			Err:     errors.New(strings.TrimSpace(message)),
		}
	}

	if turnHasAssistantMessage(target) {
		return recoveredTurnOutcome{
			Done:    true,
			Summary: "远端线程里已经有助手回复，已直接用远端内容补全当前会话。",
		}
	}

	return recoveredTurnOutcome{}
}

func selectRecoveredTurn(thread map[string]interface{}, session *Session, prompt string, startedAt time.Time) map[string]interface{} {
	turnValues, _ := nestedValue(thread, "turns").([]interface{})
	if len(turnValues) == 0 {
		return nil
	}

	activeTurnID := ""
	if session != nil {
		activeTurnID = strings.TrimSpace(session.ActiveTurnID)
	}
	if activeTurnID != "" {
		for i := len(turnValues) - 1; i >= 0; i-- {
			turn, _ := turnValues[i].(map[string]interface{})
			if turn == nil {
				continue
			}
			turnID := strings.TrimSpace(firstNonEmpty(
				itemField(turn, "id", "turnId", "turn_id"),
				lookupNestedString(turn, "turn.id"),
			))
			if turnID == activeTurnID {
				return turn
			}
		}
	}

	prompt = strings.TrimSpace(prompt)
	for i := len(turnValues) - 1; i >= 0; i-- {
		turn, _ := turnValues[i].(map[string]interface{})
		if turn == nil {
			continue
		}
		if prompt != "" && turnMatchesPrompt(turn, prompt) {
			return turn
		}
		if turnUpdatedAfter(turn, startedAt) {
			return turn
		}
	}

	return nil
}

func turnMatchesPrompt(turn map[string]interface{}, prompt string) bool {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return false
	}
	itemValues, _ := nestedValue(turn, "items").([]interface{})
	for _, itemValue := range itemValues {
		item, ok := itemValue.(map[string]interface{})
		if !ok || normalizeItemType(itemField(item, "type")) != "usermessage" {
			continue
		}
		content, _ := codexUserMessageParts(item)
		return strings.TrimSpace(content) == prompt
	}
	return false
}

func turnHasAssistantMessage(turn map[string]interface{}) bool {
	itemValues, _ := nestedValue(turn, "items").([]interface{})
	for _, itemValue := range itemValues {
		item, ok := itemValue.(map[string]interface{})
		if !ok || normalizeItemType(itemField(item, "type")) != "agentmessage" {
			continue
		}
		if strings.TrimSpace(itemField(item, "text")) != "" {
			return true
		}
	}
	return false
}

func turnUpdatedAfter(turn map[string]interface{}, t time.Time) bool {
	if t.IsZero() {
		return false
	}
	candidates := []interface{}{
		nestedValue(turn, "updatedAt"),
		nestedValue(turn, "createdAt"),
		nestedValue(turn, "updated_at"),
		nestedValue(turn, "created_at"),
	}
	for _, candidate := range candidates {
		switch value := candidate.(type) {
		case float64:
			if time.Unix(int64(value), 0).After(t) {
				return true
			}
		case int64:
			if time.Unix(value, 0).After(t) {
				return true
			}
		case int:
			if time.Unix(int64(value), 0).After(t) {
				return true
			}
		}
	}
	return false
}

func buildCodexHistoryTimeline(thread map[string]interface{}) ([]Message, []EventLog) {
	turnValues, _ := nestedValue(thread, "turns").([]interface{})
	if len(turnValues) == 0 {
		return []Message{}, []EventLog{}
	}

	baseUnix := time.Now().Unix()
	if updatedAt, ok := nestedValue(thread, "updatedAt").(float64); ok && updatedAt > 0 {
		baseUnix = int64(updatedAt)
	} else if createdAt, ok := nestedValue(thread, "createdAt").(float64); ok && createdAt > 0 {
		baseUnix = int64(createdAt)
	}
	baseTime := time.Unix(baseUnix, 0)

	messages := make([]Message, 0, len(turnValues)*2)
	events := make([]EventLog, 0, len(turnValues)*4)
	stepIndex := 0

	nextTime := func() time.Time {
		t := baseTime.Add(time.Duration(stepIndex) * time.Millisecond)
		stepIndex++
		return t
	}

	for _, turnValue := range turnValues {
		turn, ok := turnValue.(map[string]interface{})
		if !ok {
			continue
		}
		itemValues, _ := nestedValue(turn, "items").([]interface{})
		for _, itemValue := range itemValues {
			item, ok := itemValue.(map[string]interface{})
			if !ok {
				continue
			}
			itemType := normalizeItemType(itemField(item, "type"))
			switch itemType {
			case "usermessage":
				content, images := codexUserMessageParts(item)
				messages = append(messages, Message{
					ID:        firstNonEmpty(itemField(item, "id"), uuid.NewString()),
					Role:      "user",
					Content:   content,
					ImageURLs: images,
					CreatedAt: nextTime(),
				})
			case "agentmessage":
				messages = append(messages, Message{
					ID:        firstNonEmpty(itemField(item, "id"), uuid.NewString()),
					Role:      "assistant",
					Content:   itemField(item, "text"),
					CreatedAt: nextTime(),
				})
			case "reasoning":
				body := strings.Join(stringsFromValue(nestedValue(item, "summary")), "\n")
				if strings.TrimSpace(body) == "" {
					body = itemType
				}
				events = append(events, EventLog{
					ID:        uuid.NewString(),
					Kind:      "status",
					Category:  "step",
					StepType:  "reasoning",
					Phase:     "completed",
					Target:    "reasoning",
					Title:     "reasoning",
					Body:      body,
					CreatedAt: nextTime(),
				})
			case "commandexecution":
				title := "shell command completed"
				if status := strings.ToLower(strings.TrimSpace(itemField(item, "status"))); status == "failed" {
					title = "shell command failed"
				}
				body := strings.TrimSpace(itemField(item, "command"))
				output := strings.TrimSpace(itemField(item, "aggregatedOutput", "aggregated_output"))
				if output != "" {
					if body != "" {
						body += "\n\n"
					}
					body += output
				}
				events = append(events, EventLog{
					ID:        uuid.NewString(),
					Kind:      "command",
					Category:  "command",
					StepType:  "shell_command",
					Phase:     "completed",
					Target:    firstLine(body),
					Title:     title,
					Body:      body,
					CreatedAt: nextTime(),
				})
			case "filechange":
				body := summarizeFileChange(item)
				if body == "" {
					body = itemType
				}
				events = append(events, EventLog{
					ID:        uuid.NewString(),
					Kind:      "status",
					Category:  "step",
					StepType:  "filechange",
					Phase:     "completed",
					Target:    body,
					Title:     "file change",
					Body:      body,
					CreatedAt: nextTime(),
				})
			case "websearch":
				body := summarizeWebSearch(item)
				if body == "" {
					body = itemType
				}
				events = append(events, EventLog{
					ID:        uuid.NewString(),
					Kind:      "status",
					Category:  "step",
					StepType:  "websearch",
					Phase:     "completed",
					Target:    body,
					Title:     "web search",
					Body:      body,
					CreatedAt: nextTime(),
				})
			}
		}

		if strings.EqualFold(strings.TrimSpace(itemField(turn, "status")), "failed") {
			message := firstNonEmpty(
				lookupNestedString(turn, "error.message"),
				itemField(turn, "error", "message"),
				"任务执行失败",
			)
			messages = append(messages, Message{
				ID:        uuid.NewString(),
				Role:      "system",
				Content:   "任务执行失败：" + strings.TrimSpace(message),
				CreatedAt: nextTime(),
			})
		}
	}

	return messages, events
}

func codexUserMessageParts(item map[string]interface{}) (string, []string) {
	contentValues, _ := nestedValue(item, "content").([]interface{})
	if len(contentValues) == 0 {
		return "", nil
	}

	textParts := make([]string, 0, len(contentValues))
	imageURLs := make([]string, 0)
	for _, value := range contentValues {
		part, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		switch normalizeItemType(itemField(part, "type")) {
		case "text":
			if text := itemField(part, "text"); strings.TrimSpace(text) != "" {
				textParts = append(textParts, text)
			}
		case "image":
			if url := itemField(part, "url", "path"); strings.TrimSpace(url) != "" {
				imageURLs = append(imageURLs, url)
			}
		case "localimage":
			if path := itemField(part, "path"); strings.TrimSpace(path) != "" {
				imageURLs = append(imageURLs, path)
			}
		}
	}
	return strings.TrimSpace(strings.Join(textParts, "\n")), imageURLs
}

func (s *sessionStore) appendEvent(sessionID, kind, title, body string) {
	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}

	category, stepType, phase, target, count := eventFields(kind, title, body)

	logEntry := EventLog{
		ID:        uuid.NewString(),
		Kind:      kind,
		Category:  category,
		StepType:  stepType,
		Phase:     phase,
		Target:    target,
		Count:     count,
		Title:     title,
		Body:      body,
		CreatedAt: time.Now(),
	}
	rt.session.Events = append(rt.session.Events, logEntry)
	rt.session.UpdatedAt = time.Now()
	clients := cloneClients(rt.clients)
	s.mu.Unlock()

	broadcastJSON(clients, serverEvent{Type: "log", Log: &logEntry})
}

func (s *sessionStore) appendEventToProviderSessions(providerID, kind, title, body string) {
	providerID = strings.TrimSpace(strings.ToLower(providerID))
	if providerID == "" {
		return
	}

	s.mu.RLock()
	sessionIDs := make([]string, 0, len(s.sessions))
	for sessionID, rt := range s.sessions {
		if firstNonEmpty(strings.TrimSpace(rt.session.Provider), providerCodex) == providerID {
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	s.mu.RUnlock()

	for _, sessionID := range sessionIDs {
		s.appendEvent(sessionID, kind, title, body)
	}
}

func (s *sessionStore) updateThreadID(sessionID, threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	if rt.session.CodexThreadID == threadID {
		return
	}
	rt.session.CodexThreadID = threadID
	rt.session.UpdatedAt = time.Now()
}

func (s *sessionStore) updateActiveTurn(sessionID, turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	if rt.session.ActiveTurnID == turnID {
		return
	}
	rt.session.ActiveTurnID = turnID
	rt.session.UpdatedAt = time.Now()
}

func (s *sessionStore) markStopRequested(sessionID string, requested bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	rt.stopRequested = requested
}

func (s *sessionStore) stopRequested(sessionID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	return rt.stopRequested
}

func (s *sessionStore) deleteSession(sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, errors.New("missing session id")
	}

	s.mu.Lock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return false, nil
	}
	if rt.session.ActiveTaskID != "" {
		s.mu.Unlock()
		return false, errors.New("任务执行中，先用 /stop 终止")
	}
	clients := cloneClients(rt.clients)
	session := *rt.session
	delete(s.sessions, sessionID)
	s.mu.Unlock()
	for client := range clients {
		closeClientConn(client)
	}
	if s.app != nil {
		s.app.ForgetSession(sessionID, session.CodexThreadID, session.ActiveTurnID)
	}
	return true, nil
}

func (s *sessionStore) broadcast(sessionID string, event serverEvent) {
	s.mu.RLock()
	rt, ok := s.sessions[sessionID]
	if !ok {
		s.mu.RUnlock()
		return
	}
	clients := cloneClients(rt.clients)
	s.mu.RUnlock()

	broadcastJSON(clients, event)
}

func (s *sessionStore) cloneSession(sessionID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rt, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	cp := *rt.session
	cp.RestoreRef = sessionRestoreRef(rt.session)
	cp.Messages = append([]Message(nil), rt.session.Messages...)
	cp.Events = append([]EventLog(nil), rt.session.Events...)
	if rt.session.DraftMessage != nil {
		draft := *rt.session.DraftMessage
		cp.DraftMessage = &draft
	}
	return &cp
}

func (s *sessionStore) listSessions() []sessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]sessionSummary, 0, len(s.sessions))
	for _, rt := range s.sessions {
		session := rt.session
		summary := sessionSummary{
			RestoreRef:        sessionRestoreRef(session),
			ID:                session.ID,
			Provider:          firstNonEmpty(session.Provider, providerCodex),
			Model:             firstNonEmpty(strings.TrimSpace(session.Model), defaultModelForProviderID(session.Provider)),
			Workdir:           normalizeWorkdir(session.Workdir),
			CodexThreadID:     strings.TrimSpace(session.CodexThreadID),
			ProviderSessionID: strings.TrimSpace(session.ProviderSessionID),
			UpdatedAt:         session.UpdatedAt,
			MessageCount:      len(session.Messages),
			Running:           session.ActiveTaskID != "",
		}
		for i := len(session.Messages) - 1; i >= 0; i-- {
			msg := session.Messages[i]
			if strings.TrimSpace(msg.Role) != "user" {
				continue
			}
			summary.LastMessage = compactForSummary(strings.TrimSpace(msg.Content))
			break
		}
		for i := len(session.Events) - 1; i >= 0; i-- {
			event := session.Events[i]
			if event.Category == "step" && strings.TrimSpace(event.Target) != "" {
				summary.LastEvent = compactForSummary(stepSummaryText(event))
				break
			}
		}
		items = append(items, summary)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items
}

func (s *sessionStore) hasActiveCodexTask() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, rt := range s.sessions {
		if firstNonEmpty(strings.TrimSpace(rt.session.Provider), providerCodex) != providerCodex {
			continue
		}
		if strings.TrimSpace(rt.session.ActiveTaskID) != "" {
			return true
		}
	}
	return false
}

func (s *sessionStore) findSessionByThread(threadID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for sessionID, rt := range s.sessions {
		if rt.session.CodexThreadID == threadID {
			return sessionID
		}
	}
	return ""
}

func (s *sessionStore) findActiveSessionByThread(threadID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	for sessionID, rt := range s.sessions {
		if strings.TrimSpace(rt.session.CodexThreadID) != threadID {
			continue
		}
		if strings.TrimSpace(rt.session.ActiveTaskID) != "" {
			return sessionID
		}
	}
	return ""
}

func (s *sessionStore) findSessionByTurn(turnID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ""
	}
	for sessionID, rt := range s.sessions {
		if strings.TrimSpace(rt.session.ActiveTurnID) == turnID {
			return sessionID
		}
	}
	return ""
}

func (s *sessionStore) saveMultipartImages(files []*multipart.FileHeader) ([]string, error) {
	imageIDs := make([]string, 0, len(files))
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			return nil, err
		}
		filename, err := saveUploadedFile(file, header)
		file.Close()
		if err != nil {
			return nil, err
		}
		imageIDs = append(imageIDs, filename)
	}
	return imageIDs, nil
}

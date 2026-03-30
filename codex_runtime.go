package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func newAppServerClient(store *sessionStore, url string) *appServerClient {
	client := &appServerClient{
		store:         store,
		url:           url,
		lastReadAt:    time.Now(),
		pending:       make(map[string]chan rpcPacket),
		threadSession: make(map[string]string),
		threadTurn:    make(map[string]string),
		turnSession:   make(map[string]string),
		loadedThreads: make(map[string]bool),
	}
	return client
}

func (c *appServerClient) InvalidateLoadedThreads() {
	c.mu.Lock()
	c.loadedThreads = make(map[string]bool)
	c.mu.Unlock()
}

func (c *appServerClient) markSuspect(reason string) {
	reason = strings.TrimSpace(reason)
	c.mu.Lock()
	c.suspectAt = time.Now()
	c.suspectReason = reason
	c.mu.Unlock()
}

func (c *appServerClient) clearSuspect() {
	c.mu.Lock()
	c.suspectAt = time.Time{}
	c.suspectReason = ""
	c.mu.Unlock()
}

func (c *appServerClient) ForgetSession(sessionID, threadID, turnID string) {
	sessionID = strings.TrimSpace(sessionID)
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)

	c.mu.Lock()
	defer c.mu.Unlock()

	if threadID != "" {
		delete(c.threadSession, threadID)
		delete(c.loadedThreads, threadID)
		if mappedTurn := strings.TrimSpace(c.threadTurn[threadID]); mappedTurn != "" {
			delete(c.turnSession, mappedTurn)
		}
		delete(c.threadTurn, threadID)
	}
	if turnID != "" {
		delete(c.turnSession, turnID)
	}
	if sessionID == "" {
		return
	}
	for key, value := range c.threadSession {
		if strings.TrimSpace(value) == sessionID {
			delete(c.threadSession, key)
			delete(c.loadedThreads, key)
			if mappedTurn := strings.TrimSpace(c.threadTurn[key]); mappedTurn != "" {
				delete(c.turnSession, mappedTurn)
			}
			delete(c.threadTurn, key)
		}
	}
	for key, value := range c.turnSession {
		if strings.TrimSpace(value) == sessionID {
			delete(c.turnSession, key)
		}
	}
}

func (c *appServerClient) Start() error {
	ctx, cancel := context.WithTimeout(context.Background(), appServerInitWait)
	defer cancel()
	return c.ensureConnected(ctx)
}

func (c *appServerClient) Close() {
	c.mu.Lock()
	conn := c.conn
	proc := c.proc
	c.closed = true
	c.reconnecting = false
	c.conn = nil
	c.proc = nil
	c.initialized = false
	c.suspectAt = time.Time{}
	c.suspectReason = ""
	c.pending = make(map[string]chan rpcPacket)
	c.threadTurn = make(map[string]string)
	c.turnSession = make(map[string]string)
	c.loadedThreads = make(map[string]bool)
	c.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	if proc != nil && proc.Process != nil {
		_ = proc.Process.Kill()
		_, _ = proc.Process.Wait()
	}
}

func (c *appServerClient) Restart(ctx context.Context, taskFailureMessage string) error {
	c.mu.Lock()
	conn := c.conn
	proc := c.proc
	pending := c.pending
	c.closed = false
	c.reconnecting = false
	c.conn = nil
	c.proc = nil
	c.initialized = false
	c.suspectAt = time.Time{}
	c.suspectReason = ""
	c.pending = make(map[string]chan rpcPacket)
	c.threadTurn = make(map[string]string)
	c.turnSession = make(map[string]string)
	c.loadedThreads = make(map[string]bool)
	c.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	if proc != nil && proc.Process != nil {
		_ = proc.Process.Kill()
		_, _ = proc.Process.Wait()
	}

	for id, ch := range pending {
		ch <- rpcPacket{
			ID: mustMarshalJSON(id),
			Error: &rpcError{
				Code:    -32000,
				Message: "codex app-server restarted",
			},
		}
	}

	if strings.TrimSpace(taskFailureMessage) != "" {
		c.store.failAllActiveTasks(taskFailureMessage)
	}

	return c.ensureConnected(ctx)
}

func (c *appServerClient) ensureConnected(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("codex app-server client is closed")
	}
	if c.conn != nil && c.initialized {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	if err := c.connect(ctx); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || !c.initialized {
		return errors.New("codex app-server not initialized")
	}
	return nil
}

func (c *appServerClient) connect(ctx context.Context) error {
	c.mu.Lock()
	if c.conn != nil && c.initialized {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, nil)
	if err != nil {
		if startErr := c.startProcess(); startErr != nil {
			return startErr
		}

		var dialErr error
		for {
			select {
			case <-ctx.Done():
				if dialErr != nil {
					return dialErr
				}
				return ctx.Err()
			default:
			}

			conn, _, dialErr = websocket.DefaultDialer.DialContext(ctx, c.url, nil)
			if dialErr == nil {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

	c.mu.Lock()
	c.conn = conn
	c.initialized = false
	c.lastReadAt = time.Now()
	c.suspectAt = time.Time{}
	c.suspectReason = ""
	c.pending = make(map[string]chan rpcPacket)
	c.threadTurn = make(map[string]string)
	c.turnSession = make(map[string]string)
	c.loadedThreads = make(map[string]bool)
	c.mu.Unlock()

	_ = conn.SetReadDeadline(time.Now().Add(appServerReadTimeout))
	conn.SetPongHandler(func(string) error {
		c.mu.Lock()
		if c.conn == conn {
			c.lastReadAt = time.Now()
		}
		c.mu.Unlock()
		return conn.SetReadDeadline(time.Now().Add(appServerReadTimeout))
	})

	go c.readLoop(conn)
	go c.pingLoop(conn)

	initCtx, cancel := context.WithTimeout(ctx, appServerRPCTimeout)
	defer cancel()
	if _, err := c.sendRequestWithFallbackOnConn(initCtx, "initialize", map[string]interface{}{
		"clientInfo": map[string]interface{}{
			"name":    "code-web",
			"title":   "Code Web",
			"version": "0.1.0",
		},
		"capabilities": map[string]interface{}{
			"experimentalApi": true,
		},
	}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("initialize app-server: %w", err)
	}

	c.mu.Lock()
	if c.conn == conn {
		c.initialized = true
	}
	c.mu.Unlock()
	c.clearSuspect()
	return nil
}

func (c *appServerClient) pingLoop(conn *websocket.Conn) {
	ticker := time.NewTicker(appServerPingInterval)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		active := c.conn == conn
		c.mu.Unlock()
		if !active {
			return
		}

		c.writeMu.Lock()
		err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
		c.writeMu.Unlock()
		if err != nil {
			providerLog.Warn().Err(err).Msg("app-server ping failed")
			_ = conn.Close()
			return
		}
	}
}

func (c *appServerClient) startProcess() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("codex app-server client is closed")
	}
	if c.proc != nil && c.proc.Process != nil {
		return nil
	}

	cmd := exec.Command("codex", "app-server", "--listen", c.url)
	cmd.Dir = defaultWorkdir
	cmd.Env = codexAppServerEnv()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("app-server stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("app-server stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start app-server: %w", err)
	}

	stdoutLogFile, stdoutLogPath := openProcessLogFile("codex-app-server.stdout.log")
	stderrLogFile, stderrLogPath := openProcessLogFile("codex-app-server.stderr.log")
	writeProcessLogMarker(stdoutLogFile, "process started")
	writeProcessLogMarker(stderrLogFile, "process started")

	c.proc = cmd
	go logPipe("[app-server stdout] ", stdout, stdoutLogFile, nil)
	go logPipe("[app-server stderr] ", stderr, stderrLogFile, c.handleAppServerStderrLine)
	providerLog.Info().
		Str("stdout_log", stdoutLogPath).
		Str("stderr_log", stderrLogPath).
		Msg("app-server log files attached")
	go func(local *exec.Cmd) {
		err := local.Wait()
		c.mu.Lock()
		if c.proc == local {
			c.proc = nil
		}
		c.mu.Unlock()
		if err != nil {
			providerLog.Error().
				Err(err).
				Str("stdout_log", stdoutLogPath).
				Str("stderr_log", stderrLogPath).
				Msg("app-server exited")
			writeProcessLogMarker(stderrLogFile, "process exited: "+err.Error())
			return
		}
		providerLog.Info().
			Str("stdout_log", stdoutLogPath).
			Str("stderr_log", stderrLogPath).
			Msg("app-server exited cleanly")
		writeProcessLogMarker(stderrLogFile, "process exited cleanly")
	}(cmd)
	return nil
}

type providerSettingsFile struct {
	Env map[string]string `json:"env"`
}

func codexAppServerEnv() []string {
	settingsPath := codexSettingsPath
	if !filepath.IsAbs(settingsPath) {
		settingsPath = filepath.Join(defaultWorkdir, settingsPath)
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return os.Environ()
	}

	var settings providerSettingsFile
	if err := json.Unmarshal(raw, &settings); err != nil || len(settings.Env) == 0 {
		return os.Environ()
	}

	overrides := map[string]string{}
	for key, value := range settings.Env {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		overrides[strings.ToUpper(key)] = value
	}

	base := os.Environ()
	out := make([]string, 0, len(base)+len(overrides))
	seen := map[string]bool{}
	for _, item := range base {
		parts := strings.SplitN(item, "=", 2)
		key := strings.TrimSpace(parts[0])
		upper := strings.ToUpper(key)
		if value, ok := overrides[upper]; ok {
			out = append(out, key+"="+value)
			seen[upper] = true
			continue
		}
		out = append(out, item)
		seen[upper] = true
	}

	for key, value := range settings.Env {
		upper := strings.ToUpper(strings.TrimSpace(key))
		if upper == "" || seen[upper] {
			continue
		}
		out = append(out, strings.TrimSpace(key)+"="+value)
	}

	return out
}

func openProcessLogFile(name string) (*os.File, string) {
	path := filepath.Join(defaultWorkdir, logsDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		providerLog.Warn().Err(err).Str("path", path).Msg("create process log dir failed")
		return nil, path
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		providerLog.Warn().Err(err).Str("path", path).Msg("open process log file failed")
		return nil, path
	}
	return file, path
}

func writeProcessLogMarker(file *os.File, message string) {
	if file == nil {
		return
	}
	_, _ = file.WriteString(fmt.Sprintf("\n[%s] %s\n", time.Now().Format(time.RFC3339), strings.TrimSpace(message)))
}

func logPipe(prefix string, r io.Reader, sink io.WriteCloser, onLine func(string)) {
	if sink != nil {
		defer sink.Close()
	}
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if sink != nil {
				if _, writeErr := sink.Write(buf[:n]); writeErr != nil {
					providerLog.Warn().Err(writeErr).Str("stream", strings.TrimSpace(prefix)).Msg("write process log failed")
					_ = sink.Close()
					sink = nil
				}
			}
			text := strings.TrimSpace(string(buf[:n]))
			if text != "" {
				for _, line := range strings.Split(text, "\n") {
					line = strings.TrimSpace(line)
					if line != "" {
						if onLine != nil {
							onLine(line)
						}
						providerLog.Debug().Str("stream", strings.TrimSpace(prefix)).Str("line", line).Msg("process output")
					}
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (c *appServerClient) handleAppServerStderrLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	lower := strings.ToLower(line)
	if !strings.Contains(lower, "error") &&
		!strings.Contains(lower, "fatal") &&
		!strings.Contains(lower, "disconnect") &&
		!strings.Contains(lower, "timeout") &&
		!strings.Contains(lower, "connectionreset") &&
		!strings.Contains(lower, "connection reset") {
		return
	}

	body := ""
	switch {
	case strings.Contains(lower, "chatgpt.com/backend-api"),
		strings.Contains(lower, "stream disconnected before completion"),
		strings.Contains(lower, "connectionreset"),
		strings.Contains(lower, "connection reset"),
		strings.Contains(lower, "worker quit with fatal"),
		strings.Contains(lower, "failed to refresh available models"):
		body = line
	}
	if body == "" {
		return
	}

	c.mu.Lock()
	duplicate := c.lastErrorText == body && time.Since(c.lastErrorAt) < 30*time.Second
	if !duplicate {
		c.lastErrorText = body
		c.lastErrorAt = time.Now()
	}
	c.mu.Unlock()
	if duplicate {
		return
	}

	c.store.appendEventToProviderSessions(providerCodex, "status", "后端服务返回异常", body)
}

func (c *appServerClient) readLoop(conn *websocket.Conn) {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			providerLog.Err(err).Msg("app-server read error")
			c.handleDisconnect(conn, err)
			return
		}
		if c.conn == conn {
			c.lastReadAt = time.Now()
		}
		_ = conn.SetReadDeadline(time.Now().Add(appServerReadTimeout))

		var packet rpcPacket
		if err := json.Unmarshal(raw, &packet); err != nil {
			providerLog.Warn().Err(err).Msg("decode app-server packet failed")
			continue
		}
		//fmt.Println(packet.Method, string(packet.ID))
		if packet.Method != "" {
			c.handleNotification(packet.Method, packet.Params)
			continue
		}

		id := packetID(packet.ID)
		if id == "" {
			continue
		}

		c.mu.Lock()
		ch := c.pending[id]
		if ch != nil {
			delete(c.pending, id)
		}
		c.mu.Unlock()

		if ch != nil {
			ch <- packet
		}
	}
}

func (c *appServerClient) handleDisconnect(conn *websocket.Conn, err error) {
	providerLog.Warn().Err(err).Msg("app-server disconnected")
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}

	pending := c.pending
	c.pending = make(map[string]chan rpcPacket)
	c.conn = nil
	c.initialized = false
	c.threadTurn = make(map[string]string)
	c.turnSession = make(map[string]string)
	c.loadedThreads = make(map[string]bool)
	closed := c.closed
	c.mu.Unlock()

	for id, ch := range pending {
		ch <- rpcPacket{
			ID: mustMarshalJSON(id),
			Error: &rpcError{
				Code:    -32000,
				Message: "codex app-server disconnected",
			},
		}
	}

	if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		providerLog.Warn().Err(err).Msg("app-server websocket closed")
	}
	if closed {
		return
	}
	c.store.appendEventToProviderSessions(providerCodex, "status", "与后端服务的连接已断开", "实时连接已经丢失，正在后台自动重连。")
	c.scheduleReconnect()
}

func (c *appServerClient) scheduleReconnect() {
	c.mu.Lock()
	if c.closed || c.reconnecting {
		c.mu.Unlock()
		return
	}
	c.reconnecting = true
	c.mu.Unlock()

	go func() {
		delay := appServerRetryDelay
		attempt := 1
		for {
			c.mu.Lock()
			if c.closed {
				c.reconnecting = false
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()

			ctx, cancel := context.WithTimeout(context.Background(), appServerInitWait)
			err := c.ensureConnected(ctx)
			cancel()
			if err == nil {
				c.mu.Lock()
				c.reconnecting = false
				c.mu.Unlock()
				providerLog.Info().Msg("app-server reconnected")
				c.store.appendEventToProviderSessions(providerCodex, "status", "已重新连接后端服务", "实时连接已经恢复，后续任务会继续同步。")
				return
			}

			providerLog.Warn().
				Err(err).
				Int("attempt", attempt).
				Dur("retry_in", delay).
				Msg("app-server reconnect failed")
			time.Sleep(delay)
			if delay < appServerRetryMaxDelay {
				delay *= 2
				if delay > appServerRetryMaxDelay {
					delay = appServerRetryMaxDelay
				}
			}
			attempt++
		}
	}()
}

func (c *appServerClient) handleNotification(method string, raw json.RawMessage) {
	if !slices.Contains([]string{
		"thread/started",
		"turn/started",
		"item/started",
		"item/agentMessage/delta",
		"item/completed",
		"turn/completed",
		"turn/failed",
		"turn/plan/updated",
		//"item/commandExecution/outputDelta",
		"item/reasoning/summaryTextDelta",
		"item/reasoning/summaryPartAdded",
		"item/fileChange/outputDelta",
		"item/mcpToolCall/progress",
		//"mcpServer/startupStatus/updated",
		"configWarning",
		"error",
	}, method) {
		return
	}
	var payload notificationEnvelope
	if err := json.Unmarshal(raw, &payload); err != nil {
		providerLog.Warn().Err(err).Str("method", method).Msg("decode app-server notification failed")
		return
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		data = map[string]interface{}{}
	}
	threadID := notificationThreadID(payload)
	turnID := notificationTurnID(payload)
	itemType, _ := notificationItemMethod(method, payload)

	switch method {
	case "thread/started":
		if threadID != "" {
			c.recordThreadSession("", threadID, true)
		}
	case "turn/started":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			c.recordThreadTurn(threadID, turnID)
			c.recordTurnSession(sessionID, turnID)
			c.store.updateActiveTurn(sessionID, payload.TurnID)
			c.store.appendEvent(sessionID, "status", "turn started", "")
			if c.store.stopRequested(sessionID) {
				go func(sessionID string) {
					ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
					defer cancel()
					if err := c.InterruptTurn(ctx, sessionID); err != nil {
						providerLog.Warn().Err(err).Str("session_id", sessionID).Msg("auto interrupt failed")
						return
					}
					c.store.appendEvent(sessionID, "status", "turn interrupted", "stop request applied")
				}(sessionID)
			}
		}
	case "item/started":
		c.handleItemStarted(payload, itemType)
	case "item/agentMessage/delta":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" && payload.Delta != "" {
			c.store.appendAssistantDelta(sessionID, payload.ItemID, payload.Delta)
		}
	case "item/completed":
		c.handleItemCompleted(payload, itemType)
	case "turn/plan/updated":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			body := summarizePlanUpdate(data)
			if body == "" {
				body = "plan updated"
			}
			c.store.appendEvent(sessionID, "status", "plan updated", body)
		}
	case "item/commandExecution/outputDelta":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			delta := strings.TrimSpace(firstNonEmpty(payload.Delta, stringField(data, "delta")))
			if delta != "" {
				c.store.appendEvent(sessionID, "command", "shell command output", delta)
			}
		}
	case "item/reasoning/summaryTextDelta":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			delta := strings.TrimSpace(firstNonEmpty(payload.Delta, stringField(data, "delta")))
			if delta != "" {
				c.store.appendEvent(sessionID, "status", "reasoning", delta)
			}
		}
	case "item/reasoning/summaryPartAdded":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			indexText := strings.TrimSpace(stringField(data, "summaryIndex"))
			body := "summary part added"
			if indexText != "" {
				body = "summary part " + indexText + " added"
			}
			c.store.appendEvent(sessionID, "status", "reasoning", body)
		}
	case "item/fileChange/outputDelta":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			delta := strings.TrimSpace(firstNonEmpty(payload.Delta, stringField(data, "delta")))
			if delta != "" {
				c.store.appendEvent(sessionID, "status", "file change", delta)
			}
		}
	case "item/mcpToolCall/progress":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			message := strings.TrimSpace(firstNonEmpty(payload.Message, stringField(data, "message")))
			if message != "" {
				c.store.appendEvent(sessionID, "status", "mcp tool progress", message)
			}
		}
	case "mcpServer/startupStatus/updated":
		name := strings.TrimSpace(firstNonEmpty(payload.Name, stringField(data, "name")))
		status := strings.TrimSpace(stringField(data, "status"))
		errText := strings.TrimSpace(firstNonEmpty(payload.Message, stringField(data, "error")))
		body := strings.TrimSpace(name)
		if status != "" {
			if body != "" {
				body += " · "
			}
			body += status
		}
		if errText != "" {
			if body != "" {
				body += "\n"
			}
			body += errText
		}
		if body == "" {
			body = "mcp server status updated"
		}
		c.store.appendEventToProviderSessions(providerCodex, "status", "mcp server status", body)
	case "configWarning":
		body := summarizeConfigWarning(data)
		if body == "" {
			body = "config warning"
		}
		c.store.appendEventToProviderSessions(providerCodex, "status", "config warning", body)
	case "turn/completed":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			providerLog.Info().
				Str("session_id", sessionID).
				Str("thread_id", threadID).
				Str("turn_id", turnID).
				Msg("finishing task from turn completed")
			c.clearThreadTurn(threadID)
			c.clearTurnSession(turnID)
			c.store.updateActiveTurn(sessionID, "")
			c.store.appendEvent(sessionID, "status", "turn completed", "")
			c.store.finishActiveTaskOK(sessionID)
		}
	case "turn/failed":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			c.clearThreadTurn(threadID)
			c.clearTurnSession(turnID)
			c.store.updateActiveTurn(sessionID, "")
			message := strings.TrimSpace(payload.Message)
			if message == "" {
				message = "任务执行失败"
			}
			c.store.finishActiveTaskWithError(sessionID, errors.New(message))
		}
	case "error":
		if sessionID := c.sessionIDForNotification(payload); sessionID != "" {
			c.clearThreadTurn(threadID)
			c.clearTurnSession(turnID)
			c.store.updateActiveTurn(sessionID, "")
			message := extractAppServerErrorMessage(raw, payload)
			if message == "" {
				message = "codex app-server returned an error"
			}
			providerLog.Error().
				Str("session_id", sessionID).
				Str("message", message).
				Str("raw", strings.TrimSpace(string(raw))).
				Msg("app-server returned an error")
			c.store.finishActiveTaskWithError(sessionID, errors.New(message))
		}
	default:
		return
	}
}

func (c *appServerClient) handleItemStarted(payload notificationEnvelope, itemType string) {
	sessionID := c.sessionIDForNotification(payload)
	if sessionID == "" {
		return
	}
	itemType = firstNonEmpty(itemType, normalizeItemType(stringField(payload.Item, "type")))
	switch itemType {
	case "commandexecution", "command_execution":
		c.store.appendEvent(sessionID, "command", "shell command started", strings.TrimSpace(stringField(payload.Item, "command")))
	default:
		if itemType != "" {
			body := strings.TrimSpace(summarizeItemDetails(itemType, payload.Item))
			if body == "" {
				body = itemType
			}
			c.store.appendEvent(sessionID, "status", itemStartedTitle(itemType), body)
		}
	}
}

func (c *appServerClient) handleItemCompleted(payload notificationEnvelope, itemType string) {
	sessionID := c.sessionIDForNotification(payload)
	if sessionID == "" {
		return
	}

	itemType = firstNonEmpty(itemType, normalizeItemType(stringField(payload.Item, "type")))
	switch itemType {
	case "agentmessage", "agent_message":
		text := stringField(payload.Item, "text")
		c.store.completeAssistantMessage(sessionID, stringField(payload.Item, "id"), text)
	case "commandexecution", "command_execution":
		title := "shell command completed"
		if exitCode, ok := intField(payload.Item, "exitCode"); ok && exitCode != 0 {
			title = fmt.Sprintf("shell command failed (exit %d)", exitCode)
		}
		body := strings.TrimSpace(stringField(payload.Item, "command"))
		output := strings.TrimSpace(firstNonEmpty(
			stringField(payload.Item, "aggregatedOutput"),
			stringField(payload.Item, "output"),
			stringField(payload.Item, "aggregated_output"),
		))
		if output != "" {
			if body != "" {
				body += "\n\n"
			}
			body += output
		}
		c.store.appendEvent(sessionID, "command", title, body)
	default:
		if itemType != "" {
			c.store.appendEvent(sessionID, "status", "item completed", itemType)
		}
	}
}

func notificationThreadID(payload notificationEnvelope) string {
	threadID := strings.TrimSpace(payload.ThreadID)
	if threadID != "" {
		return threadID
	}
	for _, value := range []string{
		strings.TrimSpace(stringField(payload.Thread, "id")),
		strings.TrimSpace(stringField(payload.Turn, "threadId")),
		strings.TrimSpace(stringField(payload.Turn, "thread_id")),
		strings.TrimSpace(stringField(payload.Turn, "thread.id")),
		strings.TrimSpace(stringField(payload.Item, "threadId")),
		strings.TrimSpace(stringField(payload.Item, "thread_id")),
		strings.TrimSpace(stringField(payload.Item, "thread.id")),
	} {
		if value != "" {
			return value
		}
	}
	return ""
}

func notificationTurnID(payload notificationEnvelope) string {
	for _, value := range []string{
		strings.TrimSpace(payload.TurnID),
		strings.TrimSpace(stringField(payload.Turn, "id")),
		strings.TrimSpace(stringField(payload.Item, "turnId")),
		strings.TrimSpace(stringField(payload.Item, "turn_id")),
		strings.TrimSpace(stringField(payload.Item, "turn.id")),
	} {
		if value != "" {
			return value
		}
	}
	return ""
}

func notificationItemMethod(method string, payload notificationEnvelope) (string, string) {
	method = strings.TrimSpace(method)
	if method == "" {
		return normalizeItemType(stringField(payload.Item, "type")), ""
	}

	parts := strings.Split(method, "/")
	if len(parts) < 2 || parts[0] != "item" {
		return normalizeItemType(stringField(payload.Item, "type")), ""
	}
	if len(parts) == 2 {
		return normalizeItemType(stringField(payload.Item, "type")), normalizeItemType(parts[1])
	}

	itemType := normalizeItemType(parts[1])
	action := normalizeItemType(parts[len(parts)-1])
	if itemType == "" {
		itemType = normalizeItemType(stringField(payload.Item, "type"))
	}
	return itemType, action
}

func (c *appServerClient) sessionIDForNotification(payload notificationEnvelope) string {
	threadID := notificationThreadID(payload)
	turnID := notificationTurnID(payload)

	if sessionID := c.sessionIDForThread(threadID); sessionID != "" {
		if threadID != "" {
			c.recordThreadSession(sessionID, threadID, false)
		}
		if turnID != "" {
			c.recordTurnSession(sessionID, turnID)
		}
		return sessionID
	}
	if sessionID := c.sessionIDForTurn(turnID); sessionID != "" {
		if threadID != "" {
			c.recordThreadSession(sessionID, threadID, false)
		}
		return sessionID
	}
	if sessionID := c.store.findSessionByTurn(turnID); sessionID != "" {
		if threadID != "" {
			c.recordThreadSession(sessionID, threadID, false)
		}
		if turnID != "" {
			c.recordTurnSession(sessionID, turnID)
		}
		return sessionID
	}
	if sessionID := c.store.findActiveSessionByThread(threadID); sessionID != "" {
		if threadID != "" {
			c.recordThreadSession(sessionID, threadID, false)
		}
		if turnID != "" {
			c.recordTurnSession(sessionID, turnID)
		}
		providerLog.Info().
			Str("session_id", sessionID).
			Str("thread_id", threadID).
			Str("turn_id", turnID).
			Msg("recovered session mapping from active thread")
		return sessionID
	}
	return ""
}

func (c *appServerClient) sessionIDForThread(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}

	c.mu.Lock()
	sessionID := c.threadSession[threadID]
	c.mu.Unlock()
	if sessionID != "" {
		return sessionID
	}

	sessionID = c.store.findSessionByThread(threadID)
	if sessionID != "" {
		c.recordThreadSession(sessionID, threadID, true)
	}
	return sessionID
}

func (c *appServerClient) sessionIDForTurn(turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ""
	}

	c.mu.Lock()
	sessionID := c.turnSession[turnID]
	c.mu.Unlock()
	return sessionID
}

func (c *appServerClient) StartTurn(ctx context.Context, sessionID, taskID, workdir, prompt string, imagePaths []string) (string, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return "", err
	}

	threadID, err := c.ensureThread(ctx, sessionID, workdir)
	if err != nil {
		return "", err
	}
	c.recordThreadSession(sessionID, threadID, true)

	input := make([]map[string]interface{}, 0, len(imagePaths)+1)
	if strings.TrimSpace(prompt) != "" {
		input = append(input, map[string]interface{}{
			"type":          "text",
			"text":          strings.TrimSpace(prompt),
			"text_elements": []interface{}{},
		})
	}
	for _, path := range imagePaths {
		input = append(input, map[string]interface{}{
			"type": "localImage",
			"path": path,
		})
	}
	if len(input) == 0 {
		return "", errors.New("message is empty")
	}

	params := map[string]interface{}{
		"threadId": threadID,
		"input":    input,
	}
	if model := c.store.sessionModel(sessionID); model != "" {
		params["model"] = model
	}
	_, err = c.sendRequestWithFallback(ctx, "turn/start", params)
	if err != nil {
		return "", err
	}

	c.store.appendEvent(sessionID, "status", "task submitted", fmt.Sprintf("task id: %s", taskID))
	return threadID, nil
}

func (c *appServerClient) ReadRateLimits(ctx context.Context) (*rateLimitsData, error) {
	result, err := c.sendRequest(ctx, "account/rateLimits/read", map[string]interface{}{}, true)
	if err != nil {
		return nil, err
	}

	var parsed rateLimitsResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, fmt.Errorf("parse account/rateLimits/read result: %w", err)
	}
	return &parsed.RateLimits, nil
}

func (c *appServerClient) ListModels(ctx context.Context) ([]modelInfo, error) {
	result, err := c.sendRequest(ctx, "model/list", map[string]interface{}{
		"cursor":        nil,
		"limit":         50,
		"includeHidden": false,
	}, true)
	if err != nil {
		return nil, err
	}
	var parsed modelListResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, fmt.Errorf("parse model/list result: %w", err)
	}
	return parsed.Data, nil
}

func (c *appServerClient) ReadThread(ctx context.Context, threadID string) (map[string]interface{}, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("missing thread id")
	}
	result, err := c.sendRequest(ctx, "thread/read", map[string]interface{}{
		"threadId":     threadID,
		"includeTurns": true,
	}, true)
	if err != nil {
		return nil, err
	}
	var parsed threadReadResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, fmt.Errorf("parse thread/read result: %w", err)
	}
	return parsed.Thread, nil
}

func (c *appServerClient) ReadThreadViaIsolatedCLI(ctx context.Context, threadID string) (map[string]interface{}, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("missing thread id")
	}

	url, err := isolatedAppServerURL()
	if err != nil {
		return nil, err
	}

	temp := newAppServerClient(c.store, url)
	defer temp.Close()

	if err := temp.ensureConnected(ctx); err != nil {
		return nil, fmt.Errorf("start isolated codex app-server: %w", err)
	}
	thread, err := temp.ReadThread(ctx, threadID)
	if err != nil {
		return nil, fmt.Errorf("isolated thread/read failed: %w", err)
	}
	return thread, nil
}

func isolatedAppServerURL() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("reserve app-server port: %w", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return "", errors.New("reserve app-server port: unexpected listener address")
	}
	port := addr.Port
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("release reserved app-server port: %w", err)
	}
	return fmt.Sprintf("ws://127.0.0.1:%d", port), nil
}

func (c *appServerClient) ReadServiceTier(ctx context.Context) (string, error) {
	result, err := c.sendRequest(ctx, "config/read", map[string]interface{}{
		"includeLayers": false,
	}, true)
	if err != nil {
		return "", err
	}
	var parsed configReadResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return "", fmt.Errorf("parse config/read result: %w", err)
	}
	return strings.TrimSpace(parsed.Config.ServiceTier), nil
}

func (c *appServerClient) WriteConfigValue(ctx context.Context, keyPath, value string) error {
	_, err := c.sendRequest(ctx, "config/value/write", map[string]interface{}{
		"keyPath":       keyPath,
		"value":         value,
		"mergeStrategy": "upsert",
	}, true)
	return err
}

func (c *appServerClient) ClearConfigValue(ctx context.Context, keyPath string) error {
	_, err := c.sendRequest(ctx, "config/value/write", map[string]interface{}{
		"keyPath":       keyPath,
		"value":         nil,
		"mergeStrategy": "upsert",
	}, true)
	return err
}

func (c *appServerClient) ensureThread(ctx context.Context, sessionID, workdir string) (string, error) {
	session := c.store.cloneSession(sessionID)
	if session == nil {
		return "", errors.New("session not found")
	}
	workdir = normalizeWorkdir(workdir)

	if session.CodexThreadID == "" {
		params := map[string]interface{}{
			"cwd":                    workdir,
			"persistExtendedHistory": true,
		}
		if model := c.store.sessionModel(sessionID); model != "" {
			params["model"] = model
		}
		result, err := c.sendRequestWithFallback(ctx, "thread/start", params)
		if err != nil {
			return "", err
		}

		var parsed threadStartResult
		if err := json.Unmarshal(result, &parsed); err != nil {
			return "", fmt.Errorf("parse thread/start result: %w", err)
		}
		threadID := strings.TrimSpace(parsed.Thread.ID)
		if threadID == "" {
			return "", errors.New("thread/start response missing thread id")
		}
		c.store.updateThreadID(sessionID, threadID)
		c.recordThreadSession(sessionID, threadID, true)
		return threadID, nil
	}

	c.recordThreadSession(sessionID, session.CodexThreadID, false)

	c.mu.Lock()
	loaded := c.loadedThreads[session.CodexThreadID]
	c.mu.Unlock()
	if loaded {
		return session.CodexThreadID, nil
	}

	params := map[string]interface{}{
		"threadId":               session.CodexThreadID,
		"cwd":                    workdir,
		"persistExtendedHistory": true,
	}
	if model := c.store.sessionModel(sessionID); model != "" {
		params["model"] = model
	}
	if _, err := c.sendRequestWithFallback(ctx, "thread/resume", params); err != nil {
		return "", err
	}

	c.recordThreadSession(sessionID, session.CodexThreadID, true)
	return session.CodexThreadID, nil
}

func (c *appServerClient) recordThreadSession(sessionID, threadID string, loaded bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(sessionID) != "" {
		c.threadSession[threadID] = sessionID
	}
	if loaded {
		c.loadedThreads[threadID] = true
	}
}

func (c *appServerClient) recordThreadTurn(threadID, turnID string) {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	c.mu.Lock()
	c.threadTurn[threadID] = turnID
	c.mu.Unlock()
}

func (c *appServerClient) recordTurnSession(sessionID, turnID string) {
	sessionID = strings.TrimSpace(sessionID)
	turnID = strings.TrimSpace(turnID)
	if sessionID == "" || turnID == "" {
		return
	}
	c.mu.Lock()
	c.turnSession[turnID] = sessionID
	c.mu.Unlock()
}

func (c *appServerClient) clearThreadTurn(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	c.mu.Lock()
	turnID := c.threadTurn[threadID]
	delete(c.threadTurn, threadID)
	delete(c.turnSession, turnID)
	c.mu.Unlock()
}

func (c *appServerClient) clearTurnSession(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	c.mu.Lock()
	delete(c.turnSession, turnID)
	c.mu.Unlock()
}

func (c *appServerClient) hasKnownActiveTurn(session *Session) bool {
	if session == nil {
		return false
	}

	threadID := strings.TrimSpace(session.CodexThreadID)
	turnID := strings.TrimSpace(session.ActiveTurnID)

	c.mu.Lock()
	defer c.mu.Unlock()

	if threadID != "" {
		if mappedTurn := strings.TrimSpace(c.threadTurn[threadID]); mappedTurn != "" {
			return true
		}
	}
	if turnID != "" {
		if mappedSession := strings.TrimSpace(c.turnSession[turnID]); mappedSession != "" {
			return true
		}
	}
	return false
}

func (c *appServerClient) InterruptTurn(ctx context.Context, sessionID string) error {
	session := c.store.cloneSession(sessionID)
	if session == nil {
		return errors.New("session not found")
	}
	threadID := strings.TrimSpace(session.CodexThreadID)
	turnID := strings.TrimSpace(session.ActiveTurnID)
	if threadID == "" || turnID == "" {
		return errors.New("no active turn to stop")
	}
	_, err := c.sendRequestWithFallback(ctx, "turn/interrupt", map[string]interface{}{
		"threadId":       threadID,
		"expectedTurnId": turnID,
	})
	return err
}

func (c *appServerClient) CompactThread(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("no thread to compact")
	}
	_, err := c.sendRequestWithFallback(ctx, "thread/compact/start", map[string]interface{}{
		"threadId": threadID,
	})
	return err
}

func (c *appServerClient) sendRequestWithFallback(ctx context.Context, method string, baseParams map[string]interface{}) (json.RawMessage, error) {
	return c.sendRequestWithFallbackInternal(ctx, method, baseParams, true)
}

func (c *appServerClient) sendRequestWithFallbackOnConn(ctx context.Context, method string, baseParams map[string]interface{}) (json.RawMessage, error) {
	return c.sendRequestWithFallbackInternal(ctx, method, baseParams, false)
}

func (c *appServerClient) sendRequestWithFallbackInternal(ctx context.Context, method string, baseParams map[string]interface{}, requireInit bool) (json.RawMessage, error) {
	attempts := []map[string]interface{}{
		mergeMaps(baseParams, map[string]interface{}{
			"approvalPolicy": "never",
			"sandboxPolicy": map[string]interface{}{
				"type": "dangerFullAccess",
			},
		}),
		mergeMaps(baseParams, map[string]interface{}{
			"approvalPolicy": "never",
			"sandbox":        "danger-full-access",
		}),
		mergeMaps(baseParams, map[string]interface{}{
			"approvalPolicy": "never",
		}),
		baseParams,
	}

	var lastErr error
	for _, params := range attempts {
		result, err := c.sendRequest(ctx, method, params, requireInit)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !shouldRetryFallback(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *appServerClient) sendRequest(ctx context.Context, method string, params map[string]interface{}, requireInit bool) (json.RawMessage, error) {
	if requireInit {
		if err := c.ensureConnected(ctx); err != nil {
			return nil, err
		}
	} else {
		c.mu.Lock()
		hasConn := c.conn != nil
		c.mu.Unlock()
		if !hasConn {
			return nil, errors.New("codex app-server websocket is not connected")
		}
	}

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, errors.New("codex app-server websocket is not connected")
	}

	id := uuid.NewString()
	ch := make(chan rpcPacket, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	packet := map[string]interface{}{
		"id":     id,
		"method": method,
		"params": params,
	}

	c.writeMu.Lock()
	err := conn.WriteJSON(packet)
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("%s: %w", method, err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, rpcCallError(method, resp.Error)
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("%s timed out: %w", method, ctx.Err())
	}
}

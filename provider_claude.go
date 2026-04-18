package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ClaudeProvider struct{}

type claudeStreamPayload struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
	IsError   bool   `json:"is_error"`
	Error     string `json:"error"`
	Event     struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
		ContentBlock struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content_block"`
	} `json:"event"`
	Message   struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func (p *ClaudeProvider) Name() string {
	return "claude"
}

func (p *ClaudeProvider) DefaultModel() string {
	return configuredDefaultModel(p.Name(), "opus")
}

func (p *ClaudeProvider) ListSessions(ctx context.Context) ([]*SessionSummary, error) {
	files, err := p.findSessionFiles()
	if err != nil {
		return nil, err
	}

	items := make([]*SessionSummary, 0, len(files))
	for _, path := range files {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		summary, err := p.readSessionSummary(path)
		if err != nil || summary == nil {
			continue
		}
		items = append(items, summary)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (p *ClaudeProvider) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	path, err := p.findSessionFileByID(sessionID)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return p.readSession(path)
}

func (p *ClaudeProvider) DeleteSession(ctx context.Context, sessionID string) error {
	path, err := p.findSessionFileByID(sessionID)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return os.Remove(path)
}

func (p *ClaudeProvider) Exec(ctx context.Context, session *Session, prompt string, imagePaths []string, onState func(*ProviderStateUpdate), onDelta func(string), onFinal func(string), onEvent func(*Event)) error {
	if len(imagePaths) > 0 {
		var builder strings.Builder
		builder.WriteString(strings.TrimSpace(prompt))
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString("Use these local image files as context:\n")
		for _, path := range imagePaths {
			if trimmed := strings.TrimSpace(path); trimmed != "" {
				builder.WriteString("- ")
				builder.WriteString(trimmed)
				builder.WriteString("\n")
			}
		}
		prompt = strings.TrimSpace(builder.String())
	}
	args := []string{
		"-p", strings.TrimSpace(prompt),
		"--verbose",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--dangerously-skip-permissions",
		"--model", func(value, fallback string) string {
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
			return fallback
		}(session.Model, p.DefaultModel()),
	}
	if providerSessionID := strings.TrimSpace(session.ProviderSessionID); providerSessionID != "" {
		args = append(args, "--resume", providerSessionID)
	} else if sessionID := strings.TrimSpace(session.ID); sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	if session.Workdir != "" {
		args = append(args, "--add-dir", session.Workdir)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	if session.Workdir != "" {
		cmd.Dir = session.Workdir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("未找到 claude 可执行文件，请先确认 Claude CLI 已安装并已加入 PATH")
		}
		return err
	}

	var finalText string
	var streamErr string
	results := make(chan error, 2)

	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		current := ""
		lastEventKey := ""
		for scanner.Scan() {
			line := scanner.Text()
			text, errText, providerSessionID := p.parseClaudeStreamLine(line)
			if errText != "" {
				streamErr = errText
			}
			if providerSessionID != "" {
				onState(&ProviderStateUpdate{ProviderSessionID: providerSessionID})
			}
			if event := p.parseClaudeEventLine(line); event != nil {
				key := event.Kind + "|" + event.StepType + "|" + event.Title + "|" + event.Target + "|" + event.Body
				if key != lastEventKey {
					onEvent(event)
					lastEventKey = key
				}
			}
			if text == "" {
				continue
			}
			next, delta := mergeStreamText(current, text)
			if delta != "" {
				onDelta(delta)
			}
			current = next
			finalText = next
		}
		results <- scanner.Err()
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 16*1024), 256*1024)
		var lines []string
		for scanner.Scan() {
			if text := strings.TrimSpace(scanner.Text()); text != "" {
				lines = append(lines, text)
			}
		}
		if len(lines) > 0 && streamErr == "" {
			streamErr = strings.Join(lines, "\n")
		}
		results <- scanner.Err()
	}()

	if err := <-results; err != nil {
		return err
	}
	if err := <-results; err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		if streamErr != "" {
			return fmt.Errorf("claude 执行失败: %s", streamErr)
		}
		return err
	}
	if streamErr != "" {
		return errors.New(streamErr)
	}
	if strings.TrimSpace(finalText) != "" {
		onFinal(strings.TrimSpace(finalText))
	}
	return nil
}

func (p *ClaudeProvider) parseClaudeEventLine(line string) *Event {
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return nil
	}
	payloadType := strings.ToLower(strings.TrimSpace(anyString(payload["type"])))

	if payloadType == "user" {
		message, _ := payload["message"].(map[string]any)
		content, _ := message["content"].([]any)
		for _, item := range content {
			block, _ := item.(map[string]any)
			if strings.ToLower(strings.TrimSpace(anyString(block["type"]))) != "tool_result" {
				continue
			}
			target, body, title, stepType := summarizeClaudeToolResult(payload["tool_use_result"], block)
			if target == "" && body == "" {
				continue
			}
			return &Event{
				ID:        newUUID(),
				Kind:      "status",
				Category:  "step",
				StepType:  stepType,
				Phase:     "completed",
				Title:     title,
				Body:      body,
				Target:    target,
				MergeKey:  anyString(block["tool_use_id"]),
				CreatedAt: time.Now(),
			}
		}
	}

	message, _ := payload["message"].(map[string]any)
	content, _ := message["content"].([]any)
	for _, item := range content {
		block, _ := item.(map[string]any)
		if strings.ToLower(strings.TrimSpace(anyString(block["type"]))) != "tool_use" {
			continue
		}
		name := anyString(block["name"])
		callID := anyString(block["id"])
		input := block["input"]
		stepType := normalizeStepType(name)
		target := extractEventTarget(input)
		body := compactEventBody(stringifyJSON(input))
		title := eventTitleForAction(name)

		switch stepType {
		case "taskupdate":
			stepType = "todo_list"
			title = "todo list"
			body = compactEventBody(formatClaudeTaskUpdateBody(input))
		case "taskcreate":
			stepType = "todo_list"
			title = "新增任务"
			body = compactEventBody(formatClaudeTaskCreateBody(input))
		case "tasklist":
			stepType = "todo_list"
			title = "任务列表"
		case "taskget":
			stepType = "todo_list"
			title = "查看任务"
		case "edit", "write", "multiedit":
			stepType = "file_change"
			title = "修改文件"
			target = firstNonEmptyString(
				extractEventTargetMap(input, "file_path", "path"),
				target,
			)
		case "read":
			title = "读取文件"
			target = firstNonEmptyString(
				extractEventTargetMap(input, "file_path", "path"),
				target,
			)
		case "grep", "glob":
			title = "检索内容"
		case "bash":
			stepType = "command_execution"
			title = "执行命令"
			target = firstNonEmptyString(
				extractEventTargetMap(input, "command", "cmd"),
				target,
			)
		case "agent":
			title = "委派任务"
		case "askuserquestion":
			title = "请求用户输入"
		case "enterplanmode":
			stepType = "todo_list"
			title = "进入计划模式"
		case "exitplanmode":
			stepType = "todo_list"
			title = "退出计划模式"
		}

		return &Event{
			ID:        newUUID(),
			Kind:      "command",
			Category:  "command",
			StepType:  stepType,
			Phase:     "started",
			Title:     title,
			Body:      body,
			Target:    target,
			MergeKey:  callID,
			CreatedAt: time.Now(),
		}
	}

	if payloadType == "result" {
		return &Event{
			ID:        newUUID(),
			Kind:      "status",
			Category:  "step",
			StepType:  "result",
			Phase:     "completed",
			Title:     "本轮输出完成",
			Body:      compactEventBody(anyString(payload["subtype"])),
			CreatedAt: time.Now(),
		}
	}
	return nil
}

func summarizeClaudeToolResult(result any, block map[string]any) (target, body, title, stepType string) {
	title = "工具结果"
	stepType = "tool_result"
	body = compactEventBody(anyString(block["content"]))
	if isError, ok := block["is_error"].(bool); ok && isError {
		title = "工具报错"
	}

	node, _ := result.(map[string]any)
	if len(node) == 0 {
		return strings.TrimSpace(target), strings.TrimSpace(body), title, stepType
	}

	if fileNode, ok := node["file"].(map[string]any); ok {
		stepType = "read_result"
		title = "读取结果"
		target = firstNonEmptyString(
			extractEventTarget(fileNode["filePath"]),
			extractEventTarget(fileNode["file_path"]),
		)
		if body == "" {
			body = compactEventBody(firstNonEmptyString(
				anyString(fileNode["content"]),
				stringifyJSON(fileNode),
			))
		}
		return strings.TrimSpace(target), strings.TrimSpace(body), title, stepType
	}

	if filenames, ok := node["filenames"].([]any); ok {
		stepType = "search_result"
		title = "检索结果"
		target = extractEventTarget(filenames)
		if body == "" {
			body = compactEventBody(stringifyJSON(filenames))
		}
		return strings.TrimSpace(target), strings.TrimSpace(body), title, stepType
	}

	stdout := strings.TrimSpace(anyString(node["stdout"]))
	stderr := strings.TrimSpace(anyString(node["stderr"]))
	if stdout != "" || stderr != "" {
		stepType = "command_result"
		title = "命令结果"
		body = compactEventBody(firstNonEmptyString(stdout, stderr))
		return strings.TrimSpace(target), strings.TrimSpace(body), title, stepType
	}

	target = extractEventTarget(node)
	if body == "" {
		body = compactEventBody(stringifyJSON(node))
	}
	return strings.TrimSpace(target), strings.TrimSpace(body), title, stepType
}

func extractEventTargetMap(value any, keys ...string) string {
	node, _ := value.(map[string]any)
	if len(node) == 0 {
		return ""
	}
	for _, key := range keys {
		if target := extractEventTarget(node[key]); target != "" {
			return target
		}
	}
	return ""
}

func formatClaudeTaskUpdateBody(value any) string {
	node, _ := value.(map[string]any)
	if len(node) == 0 {
		return stringifyJSON(value)
	}
	var parts []string
	if taskID := strings.TrimSpace(anyString(node["task_id"])); taskID != "" {
		parts = append(parts, "任务 "+taskID)
	}
	if status := strings.TrimSpace(anyString(node["status"])); status != "" {
		parts = append(parts, "状态: "+status)
	}
	if content := strings.TrimSpace(firstNonEmptyString(anyString(node["description"]), anyString(node["title"]), anyString(node["message"]))); content != "" {
		parts = append(parts, content)
	}
	if len(parts) == 0 {
		return stringifyJSON(value)
	}
	return strings.Join(parts, "\n")
}

func formatClaudeTaskCreateBody(value any) string {
	node, _ := value.(map[string]any)
	if len(node) == 0 {
		return stringifyJSON(value)
	}
	var parts []string
	if title := strings.TrimSpace(firstNonEmptyString(anyString(node["title"]), anyString(node["description"]), anyString(node["message"]))); title != "" {
		parts = append(parts, title)
	}
	if status := strings.TrimSpace(anyString(node["status"])); status != "" {
		parts = append(parts, "状态: "+status)
	}
	if len(parts) == 0 {
		return stringifyJSON(value)
	}
	return strings.Join(parts, "\n")
}

func (p *ClaudeProvider) parseClaudeStreamLine(line string) (text string, errText string, providerSessionID string) {
	var payload claudeStreamPayload
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return "", "", ""
	}
	providerSessionID = strings.TrimSpace(payload.SessionID)
	if payload.Error != "" {
		errText = strings.TrimSpace(payload.Error)
	}
	if payload.IsError && errText == "" {
		errText = strings.TrimSpace(payload.Result)
	}
	if strings.EqualFold(payload.Type, "stream_event") {
		eventType := strings.ToLower(strings.TrimSpace(payload.Event.Type))
		if eventType == "content_block_delta" && strings.EqualFold(strings.TrimSpace(payload.Event.Delta.Type), "text_delta") {
			if text := payload.Event.Delta.Text; text != "" {
				return text, errText, providerSessionID
			}
		}
		if eventType == "content_block_start" && strings.EqualFold(strings.TrimSpace(payload.Event.ContentBlock.Type), "text") {
			if text := payload.Event.ContentBlock.Text; text != "" {
				return text, errText, providerSessionID
			}
		}
		return "", errText, providerSessionID
	}
	var chunks []string
	for _, item := range payload.Message.Content {
		if strings.EqualFold(item.Type, "text") && strings.TrimSpace(item.Text) != "" {
			chunks = append(chunks, item.Text)
		}
	}
	if len(chunks) > 0 {
		return strings.Join(chunks, "\n\n"), errText, providerSessionID
	}
	if strings.EqualFold(payload.Type, "result") && strings.TrimSpace(payload.Result) != "" {
		return strings.TrimSpace(payload.Result), errText, providerSessionID
	}
	return "", errText, providerSessionID
}

func (p *ClaudeProvider) findSessionFiles() ([]string, error) {
	root, err := p.projectsDir()
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, 32)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".jsonl") {
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

func (p *ClaudeProvider) projectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

func (p *ClaudeProvider) findSessionFileByID(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("session id is empty")
	}
	files, err := p.findSessionFiles()
	if err != nil {
		return "", err
	}
	for _, path := range files {
		if strings.EqualFold(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), sessionID) {
			return path, nil
		}
	}
	return "", errors.New("claude session not found")
}

func (p *ClaudeProvider) readSessionSummary(path string) (*SessionSummary, error) {
	session, err := p.readSession(path)
	if err != nil || session == nil {
		return nil, err
	}
	return &SessionSummary{
		ID:        session.ID,
		Provider:  session.Provider,
		Model:     session.Model,
		Title:     p.firstMessageText(session.Messages),
		UpdatedAt: session.UpdatedAt,
	}, nil
}

func (p *ClaudeProvider) readSession(path string) (*Session, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	session := &Session{
		ID:                sessionID,
		ProviderSessionID: sessionID,
		Provider:          "claude",
		Model:             "sonnet",
		Messages:          make([]*Message, 0, 32),
		IsRunning:         false,
		UpdatedAt:         time.Time{},
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		ts := parseStoredTime(anyString(entry["timestamp"]))
		if ts.After(session.UpdatedAt) {
			session.UpdatedAt = ts
		}
		if cwd := normalizeWorkdir(anyString(entry["cwd"])); cwd != "" {
			session.Workdir = cwd
		}

		switch strings.TrimSpace(anyString(entry["type"])) {
		case "user":
			if msg := p.parseStoredUser(entry, ts); msg != nil {
				session.Messages = append(session.Messages, msg)
			}
		case "assistant":
			msg, model := p.parseStoredAssistant(entry, ts)
			if model != "" {
				session.Model = model
			}
			if msg != nil {
				session.Messages = append(session.Messages, msg)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if session.UpdatedAt.IsZero() {
		if info, err := os.Stat(path); err == nil {
			session.UpdatedAt = info.ModTime()
		} else {
			session.UpdatedAt = time.Now()
		}
	}
	if session.Workdir == "" {
		session.Workdir = p.decodeClaudeProjectDir(filepath.Base(filepath.Dir(path)))
	}
	return session, nil
}

func (p *ClaudeProvider) parseStoredUser(entry map[string]any, _ time.Time) *Message {
	message, ok := entry["message"].(map[string]any)
	if !ok {
		return nil
	}
	text := strings.TrimSpace(p.extractContent(message["content"]))
	if text == "" {
		return nil
	}
	createdAt := time.Now()
	return &Message{
		ID:        firstNonEmptyString(anyString(entry["uuid"]), anyString(entry["sessionId"])+"-"+createdAt.Format(time.RFC3339Nano)),
		Role:      "user",
		Content:   text,
		CreatedAt: createdAt,
	}
}

func (p *ClaudeProvider) parseStoredAssistant(entry map[string]any, _ time.Time) (*Message, string) {
	message, ok := entry["message"].(map[string]any)
	if !ok {
		return nil, ""
	}
	text := strings.TrimSpace(p.extractContent(message["content"]))
	if text == "" {
		return nil, anyString(message["model"])
	}
	createdAt := time.Now()
	return &Message{
		ID:        firstNonEmptyString(anyString(entry["uuid"]), anyString(entry["sessionId"])+"-"+createdAt.Format(time.RFC3339Nano)),
		Role:      "assistant",
		Content:   text,
		CreatedAt: createdAt,
	}, anyString(message["model"])
}

func (p *ClaudeProvider) extractContent(value any) string {
	switch node := value.(type) {
	case string:
		return strings.TrimSpace(node)
	case []any:
		parts := make([]string, 0, len(node))
		for _, item := range node {
			if block, ok := item.(map[string]any); ok && strings.EqualFold(anyString(block["type"]), "text") {
				if text := strings.TrimSpace(anyString(block["text"])); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

func (p *ClaudeProvider) decodeClaudeProjectDir(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	decoded := strings.ReplaceAll(name, "--", `\`)
	if len(decoded) >= 2 && decoded[1] == '-' {
		decoded = decoded[:1] + ":" + decoded[2:]
	}
	return decoded
}

func (p *ClaudeProvider) firstMessageText(messages []*Message) string {
	for _, msg := range messages {
		if msg != nil && strings.TrimSpace(msg.Content) != "" {
			return strings.TrimSpace(msg.Content)
		}
	}
	return ""
}

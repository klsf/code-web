package main

import (
	"fmt"
	"strings"
)

type claudeStreamMessage struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
	IsError   bool   `json:"is_error"`
	Message   struct {
		Content []struct {
			ID        string                 `json:"id"`
			Type      string                 `json:"type"`
			Text      string                 `json:"text"`
			Name      string                 `json:"name"`
			ToolUseID string                 `json:"tool_use_id"`
			Input     map[string]interface{} `json:"input"`
			Content   string                 `json:"content"`
		} `json:"content"`
	} `json:"message"`
	Error string `json:"error"`
}

func describeClaudeStreamError(msg claudeStreamMessage, rawLine string) string {
	if text := strings.TrimSpace(msg.Error); text != "" {
		return text
	}
	if text := strings.TrimSpace(msg.Result); text != "" {
		return text
	}
	if text := extractClaudeText(msg); text != "" {
		return text
	}
	parts := make([]string, 0, 3)
	if value := strings.TrimSpace(msg.Type); value != "" {
		parts = append(parts, "type="+value)
	}
	if value := strings.TrimSpace(msg.Subtype); value != "" {
		parts = append(parts, "subtype="+value)
	}
	if value := strings.TrimSpace(msg.SessionID); value != "" {
		parts = append(parts, "session_id="+value)
	}
	if len(parts) > 0 {
		return "claude task failed (" + strings.Join(parts, ", ") + ")"
	}
	if text := strings.TrimSpace(rawLine); text != "" {
		return "claude task failed: " + text
	}
	return "claude task failed"
}

func summarizeClaudeStreamMessage(msg claudeStreamMessage) string {
	parts := make([]string, 0, 6)
	if value := strings.TrimSpace(msg.Type); value != "" {
		parts = append(parts, "type="+value)
	}
	if value := strings.TrimSpace(msg.Subtype); value != "" {
		parts = append(parts, "subtype="+value)
	}
	if value := strings.TrimSpace(msg.SessionID); value != "" {
		parts = append(parts, "session_id="+value)
	}
	if value := strings.TrimSpace(msg.Error); value != "" {
		parts = append(parts, "error="+value)
	}
	if value := strings.TrimSpace(msg.Result); value != "" {
		parts = append(parts, "result="+compactForSummary(value))
	}
	if value := extractClaudeText(msg); value != "" {
		parts = append(parts, "text="+compactForSummary(value))
	}
	if value := summarizeClaudeToolMessage(msg); value != "" {
		parts = append(parts, "tool="+compactForSummary(value))
	}
	if len(parts) == 0 {
		return "empty claude stream message"
	}
	return strings.Join(parts, " ")
}

func summarizeClaudeToolMessage(msg claudeStreamMessage) string {
	parts := make([]string, 0, len(msg.Message.Content))
	for _, item := range msg.Message.Content {
		kind := strings.TrimSpace(strings.ToLower(item.Type))
		if kind != "tool_use" && kind != "tool_result" {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" && strings.TrimSpace(item.ToolUseID) != "" {
			name = item.ToolUseID
		}
		summary := summarizeClaudeToolContentItem(item)
		if summary != "" {
			parts = append(parts, summary)
		}
	}
	return strings.Join(parts, " | ")
}

func summarizeClaudeToolContentItem(item struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Text      string                 `json:"text"`
	Name      string                 `json:"name"`
	ToolUseID string                 `json:"tool_use_id"`
	Input     map[string]interface{} `json:"input"`
	Content   string                 `json:"content"`
}) string {
	kind := strings.TrimSpace(strings.ToLower(item.Type))
	name := strings.TrimSpace(item.Name)
	body := strings.TrimSpace(item.Content)
	if body == "" && len(item.Input) > 0 {
		body = strings.TrimSpace(summarizeItemDetails(normalizeItemType(name), item.Input))
	}
	switch kind {
	case "tool_use":
		if name == "" {
			return body
		}
		if body == "" {
			return name
		}
		return name + ": " + body
	case "tool_result":
		if body != "" {
			return body
		}
		return name
	default:
		return ""
	}
}

func toolEventTitle(name string) string {
	switch normalizeItemType(name) {
	case "bash":
		return "shell command started"
	case "read", "glob", "grep", "edit", "write", "multiedit", "webfetch", "websearch", "task", "todowrite":
		return itemStartedTitle(normalizeItemType(name))
	default:
		if strings.TrimSpace(name) == "" {
			return "tool started"
		}
		return strings.ToLower(strings.TrimSpace(name)) + " started"
	}
}

func toolResultTitle(name string) string {
	switch normalizeItemType(name) {
	case "bash":
		return "shell command completed"
	case "read":
		return "read file completed"
	case "glob":
		return "find files completed"
	case "grep":
		return "search text completed"
	case "edit", "multiedit":
		return "edit file completed"
	case "write":
		return "write file completed"
	case "webfetch":
		return "open url completed"
	case "websearch":
		return "web search completed"
	case "task":
		return "task completed"
	case "todowrite":
		return "todo updated"
	default:
		if strings.TrimSpace(name) == "" {
			return "tool completed"
		}
		return strings.ToLower(strings.TrimSpace(name)) + " completed"
	}
}

func (s *sessionStore) appendClaudeToolEvents(sessionID string, msg claudeStreamMessage, seen map[string]bool, toolNames map[string]string) {
	for _, item := range msg.Message.Content {
		kind := strings.TrimSpace(strings.ToLower(item.Type))
		if kind != "tool_use" && kind != "tool_result" {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if kind == "tool_use" && name != "" && strings.TrimSpace(item.ID) != "" {
			toolNames[strings.TrimSpace(item.ID)] = name
		}
		if name == "" && strings.TrimSpace(item.ToolUseID) != "" {
			name = strings.TrimSpace(toolNames[strings.TrimSpace(item.ToolUseID)])
		}
		key := strings.TrimSpace(item.ID)
		if key == "" {
			key = kind + "|" + name + "|" + strings.TrimSpace(item.Content) + "|" + compactForSummary(fmt.Sprint(item.Input))
		}
		if seen[key] {
			continue
		}
		seen[key] = true

		body := strings.TrimSpace(item.Content)
		if body == "" && len(item.Input) > 0 {
			body = strings.TrimSpace(summarizeItemDetails(normalizeItemType(name), item.Input))
		}
		switch kind {
		case "tool_use":
			if normalizeItemType(name) == "bash" {
				s.appendEvent(sessionID, "command", toolEventTitle(name), body)
			} else {
				s.appendEvent(sessionID, "status", toolEventTitle(name), body)
			}
		case "tool_result":
			if normalizeItemType(name) == "bash" {
				s.appendEvent(sessionID, "command", toolResultTitle(name), body)
			} else {
				s.appendEvent(sessionID, "status", toolResultTitle(name), body)
			}
		}
	}
}

func extractClaudeText(msg claudeStreamMessage) string {
	parts := make([]string, 0, len(msg.Message.Content))
	for _, item := range msg.Message.Content {
		if strings.TrimSpace(strings.ToLower(item.Type)) != "text" {
			continue
		}
		if text := strings.TrimSpace(item.Text); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n\n")
	}
	if strings.EqualFold(msg.Type, "result") {
		return strings.TrimSpace(msg.Result)
	}
	return ""
}

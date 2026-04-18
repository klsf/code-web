package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var patchFileLineRE = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$`)

func parseStoredTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func anyString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringifyJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSpace(string(data))
}

func normalizeStepType(value string) string {
	return strings.ToLower(strings.Trim(strings.ReplaceAll(strings.ReplaceAll(value, " ", "_"), "-", "_"), "_"))
}

func compactEventBody(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	if len(text) > 800 {
		return text[:800] + "..."
	}
	return text
}

func parseJSONValue(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return nil
	}
	return parsed
}

func extractEventTarget(value any) string {
	switch node := value.(type) {
	case nil:
		return ""
	case string:
		text := strings.TrimSpace(node)
		if text == "" {
			return ""
		}
		if parsed := parseJSONValue(text); parsed != nil {
			if target := extractEventTarget(parsed); target != "" {
				return target
			}
		}
		if target := extractShellSearchTarget(text); target != "" {
			return target
		}
		if url := firstRegexMatch(text, `https?://\S+`); url != "" {
			return url
		}
		if path := firstRegexMatch(text, `[A-Za-z]:\\[^\s"'<>|]+`); path != "" {
			return path
		}
		if path := firstRegexMatch(text, `(?:^|[\s(])(/[A-Za-z0-9._~\-\\/]+)`); path != "" {
			return strings.TrimSpace(strings.TrimPrefix(path, "("))
		}
		line := strings.TrimSpace(strings.Split(text, "\n")[0])
		if len(line) > 160 {
			return strings.TrimSpace(line[:160]) + "..."
		}
		return line
	case []any:
		for _, item := range node {
			if target := extractEventTarget(item); target != "" {
				return target
			}
		}
		return ""
	case map[string]any:
		preferredKeys := []string{
			"url", "uri", "href", "link", "location", "page", "ref_id",
			"path", "file", "filepath", "file_path", "filename",
			"command", "cmd", "query", "q", "pattern",
			"element", "text", "selector", "name", "title", "id", "uid",
		}
		for _, key := range preferredKeys {
			if value, ok := node[key]; ok {
				if target := extractEventTarget(value); target != "" {
					return target
				}
			}
		}
		for _, value := range node {
			if target := extractEventTarget(value); target != "" {
				return target
			}
		}
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(node))
	}
}

func extractPatchTargets(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	matches := patchFileLineRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return ""
	}
	seen := map[string]struct{}{}
	files := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		file := strings.TrimSpace(match[1])
		if file == "" {
			continue
		}
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		files = append(files, file)
	}
	switch len(files) {
	case 0:
		return ""
	case 1:
		return files[0]
	case 2:
		return files[0] + "，" + files[1]
	case 3:
		return files[0] + "，" + files[1] + "，" + files[2]
	default:
		return files[0] + "，" + files[1] + "，" + files[2] + fmt.Sprintf(" 等 %d 个文件", len(files))
	}
}

func extractShellSearchTarget(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	patterns := []string{
		`(?i)Select-String\s+-Pattern\s+'([^']+)'`,
		`(?i)Select-String\s+-Pattern\s+"([^"]+)"`,
		`(?i)\brg\b\s+(?:-[^\s]+\s+)*"([^"]+)"`,
		`(?i)\brg\b\s+(?:-[^\s]+\s+)*'([^']+)'`,
		`(?i)\bgrep\b\s+(?:-[^\s]+\s+)*"([^"]+)"`,
		`(?i)\bgrep\b\s+(?:-[^\s]+\s+)*'([^']+)'`,
		`(?i)\bfindstr\b\s+(?:/[^\s]+\s+)*"([^"]+)"`,
		`(?i)-Filter\s+([^\s|]+)`,
	}
	for _, pattern := range patterns {
		if match := firstRegexMatch(text, pattern); match != "" {
			return match
		}
	}
	return ""
}

func firstRegexMatch(text, pattern string) string {
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(text)
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if len(match) > 0 {
		return strings.TrimSpace(match[0])
	}
	return ""
}

func eventTitleForAction(name string) string {
	token := normalizeStepType(name)
	switch {
	case token == "reasoning":
		return "思考中"
	case strings.Contains(token, "navigate_page"), strings.Contains(token, "new_page"), strings.Contains(token, "openurl"), strings.Contains(token, "fetchurl"):
		return "访问网页"
	case strings.Contains(token, "take_snapshot"), strings.Contains(token, "browser_snapshot"):
		return "读取页面"
	case strings.Contains(token, "take_screenshot"), strings.Contains(token, "screenshot"), strings.Contains(token, "browser_take_screenshot"):
		return "截图"
	case strings.Contains(token, "click"), strings.Contains(token, "press_key"), strings.Contains(token, "hover"), strings.Contains(token, "drag"):
		return "页面交互"
	case strings.Contains(token, "fill"), strings.Contains(token, "type"), strings.Contains(token, "select_option"), strings.Contains(token, "upload_file"):
		return "填写输入"
	case strings.Contains(token, "evaluate_script"), strings.Contains(token, "browser_evaluate"), strings.Contains(token, "browser_run_code"):
		return "执行脚本"
	case strings.Contains(token, "network_request"), strings.Contains(token, "xhr"), strings.Contains(token, "websocket"), strings.Contains(token, "request"):
		return "网络请求"
	case strings.Contains(token, "shell_command"), strings.Contains(token, "exec_command"), token == "bash":
		return "执行命令"
	case strings.Contains(token, "apply_patch"), token == "edit":
		return "修改文件"
	case strings.Contains(token, "filechange"):
		return "修改文件"
	case token == "read" || strings.Contains(token, "read_file") || strings.Contains(token, "get_content"):
		return "读取文件"
	case strings.Contains(token, "write_file"), strings.Contains(token, "create_file"):
		return "写入文件"
	case strings.Contains(token, "search"), strings.Contains(token, "find"), strings.Contains(token, "grep"), strings.Contains(token, "glob"):
		return "检索内容"
	case strings.Contains(token, "update_plan"), strings.Contains(token, "todo"):
		return "更新计划"
	default:
		return strings.ReplaceAll(strings.Trim(token, "_"), "_", " ")
	}
}

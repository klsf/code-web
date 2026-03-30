package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func defaultCodexHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("user home directory is empty")
	}
	return filepath.Join(home, ".codex"), nil
}

func codexHomeDir() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return filepath.Clean(value)
	}
	path, err := defaultCodexHome()
	if err != nil {
		return filepath.Join(os.Getenv("HOME"), ".codex")
	}
	return path
}

func nextAssetVersion() string {
	return appVersion + "-" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func normalizeRestoreRef(ref *restoreRef) *restoreRef {
	if ref == nil {
		return nil
	}
	provider := strings.TrimSpace(strings.ToLower(ref.Provider))
	codexThreadID := strings.TrimSpace(ref.CodexThreadID)
	providerSessionID := strings.TrimSpace(ref.ProviderSessionID)
	if provider == "" || (codexThreadID == "" && providerSessionID == "") {
		return nil
	}
	normalized := &restoreRef{
		LocalSessionID:    strings.TrimSpace(ref.LocalSessionID),
		Provider:          provider,
		Model:             strings.TrimSpace(ref.Model),
		Workdir:           normalizeWorkdir(ref.Workdir),
		CodexThreadID:     codexThreadID,
		ProviderSessionID: providerSessionID,
	}
	normalized.RefID = firstNonEmpty(
		strings.TrimSpace(ref.RefID),
		provider+"|"+firstNonEmpty(codexThreadID, providerSessionID),
	)
	return normalized
}

func sessionRestoreRef(session *Session) *restoreRef {
	if session == nil {
		return nil
	}
	return normalizeRestoreRef(&restoreRef{
		LocalSessionID:    strings.TrimSpace(session.ID),
		Provider:          strings.TrimSpace(session.Provider),
		Model:             strings.TrimSpace(session.Model),
		Workdir:           normalizeWorkdir(session.Workdir),
		CodexThreadID:     strings.TrimSpace(session.CodexThreadID),
		ProviderSessionID: strings.TrimSpace(session.ProviderSessionID),
	})
}

func sameOriginRequest(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if !strings.EqualFold(parsed.Host, r.Host) {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func ensureCodexHome() error {
	current := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if current != "" {
		info, err := os.Stat(current)
		if err == nil && info.IsDir() {
			return nil
		}
	}

	fallback, err := defaultCodexHome()
	if err != nil {
		return fmt.Errorf("resolve codex home: %w", err)
	}
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		return fmt.Errorf("create codex home: %w", err)
	}
	if current != "" && filepath.Clean(current) != fallback {
		appLog.Warn().Str("codex_home", current).Str("fallback", fallback).Msg("invalid CODEX_HOME, using fallback")
	}
	return os.Setenv("CODEX_HOME", fallback)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiResponseEnvelope struct {
	Status  int         `json:"status"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

func normalizeAPIData(v interface{}) interface{} {
	if v == nil {
		return map[string]interface{}{}
	}
	return v
}

func writeAPIJSON(w http.ResponseWriter, data interface{}) {
	writeJSON(w, apiResponseEnvelope{
		Status:  0,
		Message: "",
		Data:    normalizeAPIData(data),
	})
}

func writeAPIJSONStatus(w http.ResponseWriter, status int, data interface{}) {
	writeJSONStatus(w, status, apiResponseEnvelope{
		Status:  0,
		Message: "",
		Data:    normalizeAPIData(data),
	})
}

func writeAPIError(w http.ResponseWriter, statusCode int, message string, data interface{}) {
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(statusCode)
	}
	writeJSONStatus(w, statusCode, apiResponseEnvelope{
		Status:  1,
		Message: message,
		Data:    normalizeAPIData(data),
	})
}

func saveUploadedFile(file multipart.File, header *multipart.FileHeader) (string, error) {
	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
	default:
		return "", errors.New("unsupported image type")
	}

	filename := uuid.NewString() + ext
	path := filepath.Join(uploadsDir, filename)
	dst, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		return "", err
	}
	return filename, nil
}

func resolveImageFiles(imageIDs []string) ([]string, []string, error) {
	urls := make([]string, 0, len(imageIDs))
	paths := make([]string, 0, len(imageIDs))
	for _, id := range imageIDs {
		id = filepath.Base(strings.TrimSpace(id))
		if id == "." || id == "" {
			continue
		}
		path := filepath.Join(uploadsDir, id)
		if _, err := os.Stat(path); err != nil {
			return nil, nil, fmt.Errorf("image not found: %s", id)
		}
		urls = append(urls, "/uploads/"+id)
		paths = append(paths, path)
	}
	return urls, paths, nil
}

func detectCodexModel() string {
	raw, err := os.ReadFile(filepath.Join(codexHomeDir(), "config.toml"))
	if err != nil {
		return "unknown"
	}

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "model") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"'")
		if value != "" {
			return value
		}
	}

	return "unknown"
}

func ensureCodexAvailable() error {
	path, err := exec.LookPath("codex")
	if err != nil {
		if runtime.GOOS == "windows" {
			return errors.New("codex executable not found in PATH; install Codex CLI and ensure `codex.exe` (or its shim) is available in PATH before starting code-web")
		}
		return errors.New("codex executable not found in PATH; install Codex CLI and ensure `codex` is available in PATH before starting code-web")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("codex executable path is empty")
	}
	return nil
}

func listInstalledSkills() ([]skillInfo, error) {
	rootBase := providerHomeDir()
	if strings.TrimSpace(rootBase) == "" {
		return []skillInfo{}, nil
	}
	root := filepath.Join(rootBase, "skills")
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []skillInfo{}, nil
		}
		return nil, err
	}
	items := make([]skillInfo, 0, 16)
	seen := make(map[string]bool)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "SKILL.md" {
			return nil
		}
		name, description, parseErr := parseSkillFrontmatter(path)
		if parseErr != nil {
			return nil
		}
		if name == "" || seen[name] {
			return nil
		}
		seen[name] = true
		items = append(items, skillInfo{
			Name:        name,
			Description: description,
			Path:        path,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func parseSkillFrontmatter(path string) (string, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", errors.New("missing frontmatter")
	}
	var name, description string
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		if strings.HasPrefix(line, "name:") {
			name = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "name:")), "\"'")
		}
		if strings.HasPrefix(line, "description:") {
			description = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "description:")), "\"'")
		}
	}
	return name, description, nil
}

func detectTaskConcurrency() int {
	cpus := runtime.NumCPU()
	switch {
	case cpus <= 2:
		return 1
	case cpus <= 4:
		return 2
	default:
		return cpus / 2
	}
}

func mergeMaps(base map[string]interface{}, extra map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func shouldRetryFallback(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "approval") ||
		strings.Contains(text, "unknown variant") ||
		strings.Contains(text, "expected one of") ||
		strings.Contains(text, "sandbox") ||
		strings.Contains(text, "on-request") ||
		strings.Contains(text, "onrequest")
}

func rpcCallError(method string, rpcErr *rpcError) error {
	if rpcErr == nil {
		return fmt.Errorf("%s failed", method)
	}
	return fmt.Errorf("%s failed: %s", method, strings.TrimSpace(rpcErr.Message))
}

func packetID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		return num.String()
	}
	return ""
}

func mustMarshalJSON(v interface{}) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func mustJSObject(v interface{}) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func stringField(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if text, ok := value.(string); ok {
				return text
			}
		}
	}
	return ""
}

func intField(m map[string]interface{}, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		}
	}
	return 0, false
}

func normalizeItemType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(value, "\n")[0])
}

func extractAppServerErrorMessage(raw json.RawMessage, payload notificationEnvelope) string {
	if msg := strings.TrimSpace(payload.Message); msg != "" {
		return msg
	}

	var envelope map[string]interface{}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		if msg := firstNonEmpty(
			lookupNestedString(envelope, "message"),
			lookupNestedString(envelope, "error.message"),
			lookupNestedString(envelope, "error.details"),
			lookupNestedString(envelope, "details"),
			lookupNestedString(envelope, "additionalDetails"),
			lookupNestedString(envelope, "error.additionalDetails"),
		); strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
	}

	return strings.TrimSpace(string(raw))
}

func lookupNestedString(data map[string]interface{}, path string) string {
	current := interface{}(data)
	for _, part := range strings.Split(path, ".") {
		node, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current, ok = node[part]
		if !ok {
			return ""
		}
	}
	if text, ok := current.(string); ok {
		return text
	}
	return ""
}

func itemField(item map[string]interface{}, paths ...string) string {
	for _, path := range paths {
		if text := strings.TrimSpace(lookupNestedString(item, path)); text != "" {
			return text
		}
	}
	return ""
}

func itemPath(item map[string]interface{}) string {
	return itemField(
		item,
		"relativePath",
		"relative_path",
		"path",
		"filePath",
		"file_path",
		"filepath",
		"targetPath",
		"target_path",
		"target",
		"args.relativePath",
		"args.relative_path",
		"args.path",
		"args.filePath",
		"args.file_path",
		"args.targetPath",
		"args.target_path",
		"args.target",
		"arguments.relativePath",
		"arguments.relative_path",
		"arguments.path",
		"arguments.filePath",
		"arguments.file_path",
		"arguments.targetPath",
		"arguments.target_path",
		"arguments.target",
		"input.relativePath",
		"input.relative_path",
		"input.path",
		"input.filePath",
		"input.file_path",
		"input.targetPath",
		"input.target_path",
		"input.target",
	)
}

func nestedValue(data map[string]interface{}, path string) interface{} {
	current := interface{}(data)
	for _, part := range strings.Split(path, ".") {
		node, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current, ok = node[part]
		if !ok {
			return nil
		}
	}
	return current
}

func stringsFromValue(value interface{}) []string {
	switch v := value.(type) {
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return nil
		}
		return []string{text}
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			switch node := item.(type) {
			case string:
				text := strings.TrimSpace(node)
				if text != "" {
					out = append(out, text)
				}
			case map[string]interface{}:
				if path := itemPath(node); path != "" {
					out = append(out, path)
					continue
				}
				if path := itemField(node, "oldPath", "old_path", "newPath", "new_path"); path != "" {
					out = append(out, path)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func countFromValue(value interface{}) int {
	switch v := value.(type) {
	case []interface{}:
		return len(v)
	case map[string]interface{}:
		return len(v)
	default:
		return 0
	}
}

func summarizeWebSearch(item map[string]interface{}) string {
	query := itemField(item, "query", "args.query", "arguments.query", "input.query")
	if query == "" {
		query = itemField(item, "pattern", "args.pattern", "arguments.pattern", "input.pattern")
	}
	domains := stringsFromValue(nestedValue(item, "domains"))
	if len(domains) == 0 {
		domains = stringsFromValue(nestedValue(item, "args.domains"))
	}
	if len(domains) == 0 {
		domains = stringsFromValue(nestedValue(item, "arguments.domains"))
	}
	if len(domains) == 0 {
		domains = stringsFromValue(nestedValue(item, "input.domains"))
	}

	resultsCount := 0
	for _, key := range []string{"results", "items", "hits", "matches", "output.results", "output.items"} {
		if count := countFromValue(nestedValue(item, key)); count > 0 {
			resultsCount = count
			break
		}
	}

	parts := make([]string, 0, 3)
	if query != "" {
		parts = append(parts, query)
	}
	if len(domains) > 0 {
		label := strings.Join(domains[:minInt(len(domains), 2)], ", ")
		if len(domains) > 2 {
			label += fmt.Sprintf(" +%d", len(domains)-2)
		}
		parts = append(parts, "domains: "+label)
	}
	if resultsCount > 0 {
		parts = append(parts, fmt.Sprintf("%d results", resultsCount))
	}
	if len(parts) > 0 {
		return strings.Join(parts, " · ")
	}
	if url := itemField(item, "url", "uri", "args.url", "arguments.url", "input.url"); url != "" {
		return url
	}
	return ""
}

func summarizeFileChange(item map[string]interface{}) string {
	action := itemField(
		item,
		"changeType",
		"change_type",
		"action",
		"type",
		"args.changeType",
		"args.change_type",
		"args.action",
		"arguments.changeType",
		"arguments.change_type",
		"arguments.action",
		"input.changeType",
		"input.change_type",
		"input.action",
	)
	oldPath := itemField(item, "oldPath", "old_path", "args.oldPath", "args.old_path", "arguments.oldPath", "arguments.old_path", "input.oldPath", "input.old_path")
	newPath := itemField(item, "newPath", "new_path", "args.newPath", "args.new_path", "arguments.newPath", "arguments.new_path", "input.newPath", "input.new_path")
	path := itemPath(item)

	switch {
	case oldPath != "" && newPath != "" && oldPath != newPath:
		if action != "" {
			return fmt.Sprintf("%s: %s -> %s", action, oldPath, newPath)
		}
		return oldPath + " -> " + newPath
	case path != "":
		if action != "" {
			return action + ": " + path
		}
		return path
	}

	for _, key := range []string{
		"changes",
		"files",
		"targets",
		"items",
		"args.changes",
		"args.files",
		"arguments.changes",
		"arguments.files",
		"input.changes",
		"input.files",
	} {
		paths := stringsFromValue(nestedValue(item, key))
		if len(paths) == 0 {
			continue
		}
		if len(paths) == 1 {
			if action != "" {
				return action + ": " + paths[0]
			}
			return paths[0]
		}
		preview := strings.Join(paths[:minInt(len(paths), 3)], ", ")
		if len(paths) > 3 {
			preview += fmt.Sprintf(" +%d more", len(paths)-3)
		}
		if action != "" {
			return fmt.Sprintf("%s: %s", action, preview)
		}
		return preview
	}

	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func summarizeItemDetails(itemType string, item map[string]interface{}) string {
	if len(item) == 0 {
		return ""
	}

	path := itemPath(item)
	query := itemField(item, "query", "args.query", "arguments.query", "input.query")
	pattern := itemField(item, "pattern", "args.pattern", "arguments.pattern", "input.pattern")
	url := itemField(item, "url", "uri", "args.url", "arguments.url", "input.url")
	command := itemField(item, "command")

	switch itemType {
	case "readfile", "read_file", "writefile", "write_file", "editfile", "edit_file", "patchfile", "patch_file", "openfile", "open_file", "viewimage", "view_image", "listdir", "list_dir", "readdir", "read_dir":
		if path != "" {
			return path
		}
	case "grep", "searchtext", "search_text":
		if pattern != "" && path != "" {
			return pattern + " in " + path
		}
		if pattern != "" {
			return pattern
		}
		if query != "" && path != "" {
			return query + " in " + path
		}
		if query != "" {
			return query
		}
		if path != "" {
			return path
		}
	case "searchfiles", "search_files", "glob", "findfiles", "find_files":
		if pattern != "" && path != "" {
			return pattern + " in " + path
		}
		if pattern != "" {
			return pattern
		}
		if query != "" && path != "" {
			return query + " in " + path
		}
		if query != "" {
			return query
		}
		if path != "" {
			return path
		}
	case "fetchurl", "fetch_url", "openurl", "open_url":
		if url != "" {
			return url
		}
	case "websearch":
		if summary := summarizeWebSearch(item); summary != "" {
			return summary
		}
	case "filechange", "file_change":
		if summary := summarizeFileChange(item); summary != "" {
			return summary
		}
	}

	for _, text := range []string{path, pattern, query, url, command} {
		if text != "" {
			return text
		}
	}
	return ""
}

func summarizePlanUpdate(data map[string]interface{}) string {
	explanation := strings.TrimSpace(lookupNestedString(data, "explanation"))
	planValues, _ := nestedValue(data, "plan").([]interface{})
	steps := make([]string, 0, len(planValues))
	for _, value := range planValues {
		step, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		text := strings.TrimSpace(stringField(step, "step"))
		if text == "" {
			continue
		}
		status := strings.TrimSpace(stringField(step, "status"))
		if status != "" {
			text = "[" + status + "] " + text
		}
		steps = append(steps, text)
	}
	parts := make([]string, 0, 2)
	if explanation != "" {
		parts = append(parts, explanation)
	}
	if len(steps) > 0 {
		preview := strings.Join(steps[:minInt(len(steps), 3)], "\n")
		if len(steps) > 3 {
			preview += fmt.Sprintf("\n+%d more", len(steps)-3)
		}
		parts = append(parts, preview)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func summarizeConfigWarning(data map[string]interface{}) string {
	summary := strings.TrimSpace(stringField(data, "summary"))
	details := strings.TrimSpace(stringField(data, "details"))
	path := strings.TrimSpace(stringField(data, "path"))
	parts := make([]string, 0, 3)
	if summary != "" {
		parts = append(parts, summary)
	}
	if path != "" {
		parts = append(parts, path)
	}
	if details != "" {
		parts = append(parts, details)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func itemStartedTitle(itemType string) string {
	switch itemType {
	case "readfile", "read_file":
		return "read file"
	case "writefile", "write_file":
		return "write file"
	case "editfile", "edit_file", "patchfile", "patch_file":
		return "edit file"
	case "searchfiles", "search_files", "glob", "findfiles", "find_files":
		return "find files"
	case "grep", "searchtext", "search_text":
		return "search text"
	case "openfile", "open_file", "viewimage", "view_image":
		return "open file"
	case "fetchurl", "fetch_url", "openurl", "open_url":
		return "open url"
	case "listdir", "list_dir", "readdir", "read_dir":
		return "list directory"
	case "websearch":
		return "web search"
	case "filechange", "file_change":
		return "file change"
	default:
		if itemType == "" {
			return "step started"
		}
		return strings.ReplaceAll(itemType, "_", " ")
	}
}

func eventFields(kind, title, body string) (string, string, string, string, int) {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	lowerTitle := strings.ToLower(title)

	if kind == "command" {
		phase := "started"
		if strings.Contains(lowerTitle, "completed") {
			phase = "completed"
		}
		if strings.Contains(lowerTitle, "failed") {
			phase = "failed"
		}
		return "command", "shell_command", phase, firstLine(body), 0
	}

	if kind != "status" {
		return "", "", "", "", 0
	}

	switch {
	case lowerTitle == "task submitted":
		return "task", "task", "submitted", body, 0
	case lowerTitle == "task queued":
		return "task", "task", "queued", body, 0
	case lowerTitle == "task dequeued":
		return "task", "task", "dequeued", body, 0
	case lowerTitle == "turn started":
		return "turn", "turn", "started", "", 0
	case lowerTitle == "turn completed":
		return "turn", "turn", "completed", "", 0
	case lowerTitle == "turn interrupted":
		return "turn", "turn", "interrupted", "", 0
	case lowerTitle == "thread compact started":
		return "thread", "compact", "started", "", 0
	case lowerTitle == "review started":
		return "review", "review", "started", "", 0
	case strings.HasSuffix(lowerTitle, " completed"):
		return "step", normalizeItemType(strings.TrimSpace(strings.TrimSuffix(lowerTitle, " completed"))), "completed", body, 0
	case strings.HasSuffix(lowerTitle, " started"):
		return "step", normalizeItemType(strings.TrimSpace(strings.TrimSuffix(lowerTitle, " started"))), "started", body, 0
	case body != "":
		return "step", normalizeItemType(lowerTitle), "started", body, 0
	default:
		return "status", normalizeItemType(lowerTitle), "", body, 0
	}
}

func stepSummaryText(event EventLog) string {
	label := strings.TrimSpace(event.Title)
	if event.StepType != "" {
		switch normalizeItemType(event.StepType) {
		case "readfile":
			label = "read file"
		case "writefile":
			label = "write file"
		case "editfile", "patchfile":
			label = "edit file"
		case "searchfiles", "glob", "findfiles":
			label = "find files"
		case "grep", "searchtext":
			label = "search text"
		case "fetchurl", "openurl":
			label = "open url"
		case "websearch":
			label = "web search"
		case "filechange":
			label = "file change"
		}
	}
	target := strings.TrimSpace(firstNonEmpty(event.Target, event.Body))
	if label == "" {
		return target
	}
	if target == "" || strings.EqualFold(target, label) {
		return label
	}
	return label + ": " + target
}

func detectServiceTier() string {
	if !supportsFastMode() {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(codexHomeDir(), "config.toml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "service_tier") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		if value != "" {
			return value
		}
	}
	return ""
}

func compactForSummary(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len(text) <= 72 {
		return text
	}
	return text[:72]
}

func normalizeWorkdir(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultWorkdir
	}
	if !filepath.IsAbs(value) {
		return defaultWorkdir
	}
	return filepath.Clean(value)
}

func validateWorkdir(value string) (string, error) {
	workdir := normalizeWorkdir(value)
	info, err := os.Stat(workdir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("工作目录不存在")
		}
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("工作目录不是目录")
	}
	return workdir, nil
}

func tierToFastArg(tier string) string {
	if strings.EqualFold(strings.TrimSpace(tier), "fast") {
		return "on"
	}
	return "off"
}

func cloneClients(src map[*clientConn]struct{}) map[*clientConn]struct{} {
	dst := make(map[*clientConn]struct{}, len(src))
	for client := range src {
		dst[client] = struct{}{}
	}
	return dst
}

func broadcastJSON(clients map[*clientConn]struct{}, event serverEvent) {
	for client := range clients {
		if !enqueueClientEvent(client, event) {
			providerLog.Warn().Msg("client send queue is full, closing websocket client")
			closeClientConn(client)
		}
	}
}

func writeClientJSON(client *clientConn, event serverEvent) error {
	if client == nil || client.conn == nil {
		return errors.New("client connection is nil")
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	_ = client.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	err := client.conn.WriteJSON(event)
	_ = client.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		_ = client.conn.Close()
	}
	return err
}

func newClientConn(conn *websocket.Conn) *clientConn {
	return &clientConn{
		conn: conn,
		send: make(chan serverEvent, 256),
	}
}

func enqueueClientEvent(client *clientConn, event serverEvent) bool {
	if client == nil {
		return false
	}
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	if client.closed || client.send == nil {
		return false
	}
	select {
	case client.send <- event:
		return true
	default:
		return false
	}
}

func startClientWriter(client *clientConn) {
	if client == nil {
		return
	}
	go func() {
		for event := range client.send {
			if err := writeClientJSON(client, event); err != nil {
				return
			}
		}
	}()
}

func closeClientConn(client *clientConn) {
	if client == nil {
		return
	}
	client.closeOnce.Do(func() {
		client.stateMu.Lock()
		client.closed = true
		send := client.send
		client.send = nil
		conn := client.conn
		client.stateMu.Unlock()
		if client.conn != nil {
			_ = conn.Close()
		}
		if send != nil {
			close(send)
		}
	})
}

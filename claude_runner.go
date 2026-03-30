package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func detectClaudeModel() string {
	return "sonnet"
}

func claudeSessionID(session *Session) string {
	if session == nil {
		return ""
	}
	if value := strings.TrimSpace(session.ProviderSessionID); value != "" {
		return value
	}
	if value := strings.TrimSpace(session.ID); value != "" {
		return value
	}
	return ""
}

func shouldUseClaudeResume(session *Session) bool {
	if session == nil {
		return false
	}
	return strings.TrimSpace(session.ProviderSessionID) != ""
}

func (s *sessionStore) runClaudeTask(sessionID, taskID, prompt string, imagePaths []string) {
	if len(imagePaths) > 0 {
		var builder strings.Builder
		builder.WriteString(strings.TrimSpace(prompt))
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString("Use these local image files as context:\n")
		for _, path := range imagePaths {
			builder.WriteString("- ")
			builder.WriteString(strings.TrimSpace(path))
			builder.WriteString("\n")
		}
		prompt = strings.TrimSpace(builder.String())
	}
	s.runClaudePromptTask(sessionID, taskID, prompt)
}

func (s *sessionStore) runClaudeReviewTask(sessionID, taskID, args string) {
	reviewPrompt := "Review the current uncommitted changes in this repository. Focus on bugs, regressions, security issues, and missing tests. List findings first with file paths and line references when possible."
	if strings.TrimSpace(args) != "" {
		reviewPrompt += "\n\nAdditional review scope: " + strings.TrimSpace(args)
	}
	s.runClaudePromptTask(sessionID, taskID, reviewPrompt)
}

func (s *sessionStore) runClaudePromptTask(sessionID, taskID, prompt string) {
	workdir := s.sessionWorkdir(sessionID)
	session := s.cloneSession(sessionID)
	if session == nil {
		s.finishTaskWithError(sessionID, taskID, errors.New("session not found"))
		return
	}
	claudeSession := claudeSessionID(session)
	resumeSession := shouldUseClaudeResume(session)

	args := []string{
		"-p", strings.TrimSpace(prompt),
		"--verbose",
		"--output-format", "stream-json",
		"--dangerously-skip-permissions",
	}
	if claudeSession != "" {
		if resumeSession {
			args = append(args, "--resume", claudeSession)
		} else {
			args = append(args, "--session-id", claudeSession)
		}
	}
	if workdir != "" {
		args = append(args, "--add-dir", workdir)
	}
	settingsPath := claudeSettingsPath
	if !filepath.IsAbs(settingsPath) {
		settingsPath = filepath.Join(defaultWorkdir, settingsPath)
	}
	executable := executableForProvider(session.Provider)
	if strings.TrimSpace(executable) == "" {
		s.finishTaskWithError(sessionID, taskID, errors.New("claude executable is not available"))
		return
	}
	if info, err := os.Stat(settingsPath); err == nil && !info.IsDir() {
		args = append(args, "--settings", settingsPath)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = workdir
	providerLog.Info().
		Str("provider", session.Provider).
		Str("executable", executable).
		Str("provider_session_id", claudeSession).
		Bool("resume", resumeSession).
		Str("workdir", workdir).
		Str("settings", func() string {
			if slices.Contains(args, "--settings") {
				return settingsPath
			}
			return ""
		}()).
		Msg("starting claude command")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		s.finishTaskWithError(sessionID, taskID, fmt.Errorf("claude stdout pipe: %w", err))
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		s.finishTaskWithError(sessionID, taskID, fmt.Errorf("claude stderr pipe: %w", err))
		return
	}
	if err := cmd.Start(); err != nil {
		cancel()
		s.finishTaskWithError(sessionID, taskID, fmt.Errorf("start claude: %w", err))
		return
	}

	s.setTaskCancel(sessionID, cancel)
	defer s.clearTaskCancel(sessionID, cancel)

	var stderrBuf bytes.Buffer
	go logPipe("[claude] ", io.TeeReader(stderr, &stderrBuf), nil, nil)

	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	draftID := "claude-" + taskID
	lastText := ""
	finalText := ""
	finalErr := ""
	lastErrorLine := ""
	seenToolEvents := map[string]bool{}
	toolNames := map[string]string{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var msg claudeStreamMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			s.appendEvent(sessionID, "status", "claude output", line)
			continue
		}
		providerLog.Debug().
			Str("provider", session.Provider).
			Str("session_id", sessionID).
			Str("task_id", taskID).
			Str("summary", summarizeClaudeStreamMessage(msg)).
			Msg("claude stream message")
		if strings.TrimSpace(msg.SessionID) != "" {
			s.updateProviderSessionID(sessionID, msg.SessionID)
		}
		s.appendClaudeToolEvents(sessionID, msg, seenToolEvents, toolNames)

		text := extractClaudeText(msg)
		if text != "" {
			if strings.HasPrefix(text, lastText) {
				s.appendAssistantDelta(sessionID, draftID, text[len(lastText):])
			} else if text != lastText {
				s.completeAssistantMessage(sessionID, draftID, text)
			}
			lastText = text
			finalText = text
		}

		if msg.IsError {
			lastErrorLine = line
			finalErr = describeClaudeStreamError(msg, line)
		}
		if strings.EqualFold(msg.Type, "result") && strings.TrimSpace(msg.Result) != "" {
			finalText = strings.TrimSpace(msg.Result)
			if msg.IsError {
				finalErr = describeClaudeStreamError(msg, line)
			}
		}
	}

	waitErr := cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil && waitErr == nil {
		waitErr = scanErr
	}
	if ctx.Err() == context.Canceled {
		s.finishTaskWithError(sessionID, taskID, errors.New("task stopped"))
		return
	}
	if waitErr != nil {
		message := strings.TrimSpace(finalErr)
		if message == "" {
			message = strings.TrimSpace(stderrBuf.String())
		}
		if message == "" {
			message = strings.TrimSpace(lastErrorLine)
		}
		if message == "" {
			message = waitErr.Error()
		}
		s.finishTaskWithError(sessionID, taskID, fmt.Errorf("claude failed: %s", message))
		return
	}
	if strings.TrimSpace(finalErr) != "" {
		s.finishTaskWithError(sessionID, taskID, errors.New(strings.TrimSpace(finalErr)))
		return
	}
	if strings.TrimSpace(finalText) != "" {
		s.completeAssistantMessage(sessionID, draftID, strings.TrimSpace(finalText))
	}
	s.finishTaskOK(sessionID, taskID)
}

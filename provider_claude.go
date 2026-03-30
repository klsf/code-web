package main

import (
	"context"
	"errors"
)

func (p *claudeProvider) DefaultModel() string {
	return detectClaudeModel()
}

func (p *claudeProvider) DefaultModels() []modelInfo {
	return []modelInfo{
		{ID: "sonnet", Model: "sonnet", DisplayName: "sonnet", Description: "Claude latest Sonnet alias", IsDefault: true},
		{ID: "opus", Model: "opus", DisplayName: "opus", Description: "Claude latest Opus alias"},
	}
}

func (p *claudeProvider) SupportsFastMode() bool {
	return false
}

func (p *claudeProvider) SupportsCompact() bool {
	return false
}

func (p *claudeProvider) SupportsRateLimits() bool {
	return false
}

func (p *claudeProvider) Start(*sessionStore) error {
	return nil
}

func (p *claudeProvider) Close() error {
	return nil
}

func (p *claudeProvider) InvalidateSession(*sessionStore, string) {}

func (p *claudeProvider) RunPrompt(store *sessionStore, sessionID, taskID, prompt string, imagePaths []string) {
	go store.runClaudeTask(sessionID, taskID, prompt, imagePaths)
}

func (p *claudeProvider) RunReview(store *sessionStore, sessionID, taskID, args string) {
	go store.runClaudeReviewTask(sessionID, taskID, args)
}

func (p *claudeProvider) StopActiveTask(store *sessionStore, sessionID string) (bool, error) {
	if store.cancelTask(sessionID) {
		store.appendEvent(sessionID, "status", "task stopped", "")
		return true, nil
	}
	return false, errors.New("no active claude task to stop")
}

func (p *claudeProvider) SetFastMode(*sessionStore, string, string) (bool, string, error) {
	return false, "", errors.New("current provider does not support /fast")
}

func (p *claudeProvider) CompactSession(*sessionStore, string) (bool, error) {
	return false, errors.New("current provider does not support /compact")
}

func (p *claudeProvider) ListModels(context.Context, *sessionStore, string) ([]modelInfo, error) {
	return p.DefaultModels(), nil
}

func (p *claudeProvider) ReadRateLimits(context.Context, *sessionStore) (*rateLimitsData, error) {
	return nil, errors.New("current provider does not expose rate limits")
}

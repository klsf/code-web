package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (p *codexProvider) DefaultModel() string {
	return detectCodexModel()
}

func (p *codexProvider) DefaultModels() []modelInfo {
	return nil
}

func (p *codexProvider) SupportsFastMode() bool {
	return true
}

func (p *codexProvider) SupportsCompact() bool {
	return true
}

func (p *codexProvider) SupportsRateLimits() bool {
	return true
}

func (p *codexProvider) Start(store *sessionStore) error {
	if err := ensureCodexHome(); err != nil {
		return fmt.Errorf("prepare codex home: %w", err)
	}
	app := newAppServerClient(store, appServerURL)
	if err := app.Start(); err != nil {
		return fmt.Errorf("start codex app-server: %w", err)
	}
	p.app = app
	store.app = app
	if tier, err := app.ReadServiceTier(context.Background()); err == nil {
		store.mu.Lock()
		store.meta.ServiceTier = strings.TrimSpace(tier)
		store.meta.FastMode = strings.EqualFold(strings.TrimSpace(tier), "fast")
		store.mu.Unlock()
	}
	return nil
}

func (p *codexProvider) Close() error {
	if p.app != nil {
		p.app.Close()
		p.app = nil
	}
	return nil
}

func (p *codexProvider) InvalidateSession(store *sessionStore, _ string) {
	if p.app == nil && store.app != nil {
		p.app = store.app
	}
	if p.app != nil {
		p.app.InvalidateLoadedThreads()
	}
}

func (p *codexProvider) RunPrompt(store *sessionStore, sessionID, taskID, prompt string, imagePaths []string) {
	go store.runAppServerTask(sessionID, taskID, prompt, imagePaths)
}

func (p *codexProvider) RunReview(store *sessionStore, sessionID, taskID, args string) {
	go store.runReviewTask(sessionID, taskID, args)
}

func (p *codexProvider) StopActiveTask(store *sessionStore, sessionID string) (bool, error) {
	session := store.cloneSession(sessionID)
	if session == nil {
		return false, errors.New("session not found")
	}
	if strings.TrimSpace(session.ActiveTurnID) == "" {
		store.markStopRequested(sessionID, true)
		store.appendEvent(sessionID, "status", "stop requested", "waiting for active turn")
		return true, nil
	}
	if store.app == nil {
		return false, errors.New("codex app-server is not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := store.app.InterruptTurn(ctx, sessionID); err != nil {
		return false, err
	}
	store.markStopRequested(sessionID, true)
	store.appendEvent(sessionID, "status", "turn interrupted", "")
	return true, nil
}

func (p *codexProvider) SetFastMode(store *sessionStore, sessionID, value string) (bool, string, error) {
	mode := strings.TrimSpace(strings.ToLower(value))
	if mode == "" {
		if store.currentFastMode() {
			mode = "off"
		} else {
			mode = "on"
		}
	}
	switch mode {
	case "status":
		return store.currentFastMode(), store.currentServiceTier(), nil
	case "on":
		if err := store.writeServiceTier("fast"); err != nil {
			return false, "", err
		}
		store.mu.Lock()
		store.meta.ServiceTier = "fast"
		store.meta.FastMode = true
		store.mu.Unlock()
		store.resetSessionThreads()
	case "off":
		if err := store.clearServiceTier(); err != nil {
			return false, "", err
		}
		store.mu.Lock()
		store.meta.ServiceTier = ""
		store.meta.FastMode = false
		store.mu.Unlock()
		store.resetSessionThreads()
	default:
		return false, "", errors.New("usage: /fast [on|off|status]")
	}
	p.InvalidateSession(store, sessionID)
	store.broadcastMetaToProviders(providerCodex)
	return store.currentFastMode(), store.currentServiceTier(), nil
}

func (p *codexProvider) CompactSession(store *sessionStore, sessionID string) (bool, error) {
	if store.app == nil {
		return false, errors.New("codex app-server is not available")
	}
	if store.activeTaskID(sessionID) != "" {
		return false, errors.New("task is running, stop it before compacting")
	}
	session := store.cloneSession(sessionID)
	if session == nil {
		return false, errors.New("session not found")
	}
	if strings.TrimSpace(session.CodexThreadID) == "" {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := store.app.CompactThread(ctx, session.CodexThreadID); err != nil {
		return false, err
	}
	store.appendEvent(sessionID, "status", "thread compact started", "")
	return true, nil
}

func (p *codexProvider) ListModels(ctx context.Context, store *sessionStore, _ string) ([]modelInfo, error) {
	if store.app == nil {
		return nil, errors.New("codex app-server is not available")
	}
	return store.app.ListModels(ctx)
}

func (p *codexProvider) ReadRateLimits(ctx context.Context, store *sessionStore) (*rateLimitsData, error) {
	if store.app == nil {
		return nil, errors.New("codex app-server is not available")
	}
	return store.app.ReadRateLimits(ctx)
}

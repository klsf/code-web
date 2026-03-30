package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

var activeProvider Provider = newCodexProvider()
var availableProviders = map[string]Provider{}

func newCodexProvider() Provider {
	return &codexProvider{
		providerBase: providerBase{
			id:           providerCodex,
			displayName:  "Codex",
			appName:      "Code Web",
			executable:   "codex",
			requiresAuth: true,
		},
	}
}

func newClaudeProvider() Provider {
	return &claudeProvider{
		providerBase: providerBase{
			id:           providerClaude,
			displayName:  "Claude",
			appName:      "Code Web",
			executable:   "claude",
			requiresAuth: false,
		},
	}
}

func allProviders() []Provider {
	return []Provider{
		newCodexProvider(),
		newClaudeProvider(),
	}
}

func ensureProviderAvailable() error {
	return activeProvider.EnsureAvailable()
}

func ensureProviderExecutable(provider Provider) error {
	return ensureProviderExecutableByFields(provider.DisplayName(), provider.Executable(), provider.AppName())
}

func ensureProviderExecutableByFields(displayName, executable, appName string) error {
	path, err := exec.LookPath(executable)
	if err != nil {
		if runtime.GOOS == "windows" {
			return fmt.Errorf("%s executable not found in PATH; ensure `%s.exe` (or its shim) is available before starting %s", displayName, executable, appName)
		}
		return fmt.Errorf("%s executable not found in PATH; ensure `%s` is available before starting %s", displayName, executable, appName)
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s executable path is empty", displayName)
	}
	return nil
}

func detectAvailableProviders() map[string]Provider {
	items := map[string]Provider{}
	for _, provider := range allProviders() {
		if provider.EnsureAvailable() == nil {
			items[provider.ID()] = provider
		}
	}
	return items
}

func selectDefaultProvider(items map[string]Provider) Provider {
	if provider, ok := items[providerCodex]; ok {
		return provider
	}
	if provider, ok := items[providerClaude]; ok {
		return provider
	}
	return newCodexProvider()
}

func providerByID(id string) (Provider, bool) {
	normalized := strings.TrimSpace(strings.ToLower(id))
	if provider, ok := availableProviders[normalized]; ok {
		return provider, true
	}
	switch normalized {
	case "", providerCodex:
		return newCodexProvider(), true
	case providerClaude, "claude-code":
		return newClaudeProvider(), true
	default:
		return nil, false
	}
}

func providerForID(providerID string) Provider {
	if provider, ok := providerByID(providerID); ok {
		return provider
	}
	return activeProvider
}

func executableForProvider(providerID string) string {
	return providerForID(providerID).Executable()
}

func availableProviderList() []providerInfo {
	items := make([]providerInfo, 0, len(allProviders()))
	for _, provider := range allProviders() {
		_, ok := availableProviders[provider.ID()]
		items = append(items, providerInfo{
			ID:          provider.ID(),
			DisplayName: provider.DisplayName(),
			Available:   ok,
			IsDefault:   provider.ID() == activeProvider.ID(),
		})
	}
	return items
}

func supportsAppServerFor(providerID string) bool {
	return providerForID(providerID).SupportsRateLimits()
}

func supportsFastModeFor(providerID string) bool {
	return providerForID(providerID).SupportsFastMode()
}

func supportsCompactFor(providerID string) bool {
	return providerForID(providerID).SupportsCompact()
}

func supportsAppServer() bool {
	return activeProvider.SupportsRateLimits()
}

func supportsFastMode() bool {
	return activeProvider.SupportsFastMode()
}

func supportsCompact() bool {
	return activeProvider.SupportsCompact()
}

func providerHomeDir() string {
	if activeProvider.ID() == providerCodex {
		return codexHomeDir()
	}
	return ""
}

func defaultModelForProviderID(providerID string) string {
	return providerForID(providerID).DefaultModel()
}

func defaultModelForProvider() string {
	return activeProvider.DefaultModel()
}

func defaultModelsForProvider(providerID string) []modelInfo {
	return providerForID(providerID).DefaultModels()
}

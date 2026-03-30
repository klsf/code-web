package main

import "context"

const (
	providerCodex  = "codex"
	providerClaude = "claude"
)

type Provider interface {
	ID() string
	DisplayName() string
	AppName() string
	Executable() string
	RequiresAuth() bool
	DefaultModel() string
	DefaultModels() []modelInfo
	SupportsFastMode() bool
	SupportsCompact() bool
	SupportsRateLimits() bool
	EnsureAvailable() error
	Start(*sessionStore) error
	Close() error
	InvalidateSession(*sessionStore, string)
	RunPrompt(*sessionStore, string, string, string, []string)
	RunReview(*sessionStore, string, string, string)
	StopActiveTask(*sessionStore, string) (bool, error)
	SetFastMode(*sessionStore, string, string) (bool, string, error)
	CompactSession(*sessionStore, string) (bool, error)
	ListModels(context.Context, *sessionStore, string) ([]modelInfo, error)
	ReadRateLimits(context.Context, *sessionStore) (*rateLimitsData, error)
}

type providerBase struct {
	id           string
	displayName  string
	appName      string
	executable   string
	requiresAuth bool
}

func (p providerBase) ID() string          { return p.id }
func (p providerBase) DisplayName() string { return p.displayName }
func (p providerBase) AppName() string     { return p.appName }
func (p providerBase) Executable() string  { return p.executable }
func (p providerBase) RequiresAuth() bool  { return p.requiresAuth }
func (p providerBase) EnsureAvailable() error {
	return ensureProviderExecutableByFields(p.displayName, p.executable, p.appName)
}

type codexProvider struct {
	providerBase
	app *appServerClient
}

type claudeProvider struct {
	providerBase
}

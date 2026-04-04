package main

import (
	"context"
)

// ProviderStateUpdate 表示 provider 在执行过程中返回的会话状态更新。
type ProviderStateUpdate struct {
	// ProviderSessionID 是 provider 返回的原生会话 ID；对 Codex 来说这里保存 thread ID。
	ProviderSessionID string
	// FinalText 是 provider 已确认完成的最终回复文本。
	FinalText string
	// IsComplete 表示 provider 已确认本轮任务完成。
	IsComplete bool
}

type Provider interface {
	Name() string
	DefaultModel() string
	Exec(ctx context.Context, session *Session, prompt string, imagePaths []string, onState func(*ProviderStateUpdate), onDelta func(string), onFinal func(string), onEvent func(*Event)) error
}

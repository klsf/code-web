package main

import "time"

type Message struct {
	// ID 是消息唯一标识，前端依赖它稳定更新流式内容。
	ID string `json:"id"`
	// Role 表示消息角色，例如 user、assistant、system。
	Role string `json:"role"`
	// Content 是消息的文本内容。
	Content string `json:"content"`
	// ImageURLs 是用户消息附带的图片访问地址列表。
	ImageURLs []string `json:"imageUrls,omitempty"`
	// CreatedAt 是消息创建时间。
	CreatedAt time.Time `json:"createdAt"`
}

type Event struct {
	// ID 是事件唯一标识，用于前端稳定渲染和去重。
	ID string `json:"id"`
	// Kind 表示事件类型，例如 status、command、tool。
	Kind string `json:"kind"`
	// Category 表示事件归类，例如 step、command。
	Category string `json:"category,omitempty"`
	// StepType 表示更细粒度的步骤类型，例如 shell_command、read_file。
	StepType string `json:"stepType,omitempty"`
	// Phase 表示步骤阶段，例如 started、completed。
	Phase string `json:"phase,omitempty"`
	// Title 是事件标题，通常用于展示工具名或动作名。
	Title string `json:"title,omitempty"`
	// Body 是事件补充说明或输出摘要。
	Body string `json:"body,omitempty"`
	// Target 表示这次步骤操作的目标对象，例如文件路径或命令名。
	Target string `json:"target,omitempty"`
	// MergeKey 用于把同一次工具调用的 started/completed 合并成同一条事件。
	MergeKey string `json:"mergeKey,omitempty"`
	// CreatedAt 是事件创建时间。
	CreatedAt time.Time `json:"createdAt"`
}

type SessionSummary struct {
	// ID 是 provider 侧会话的唯一标识。
	ID string `json:"id"`
	// Provider 表示这条会话摘要来自哪个 provider。
	Provider string `json:"provider"`
	// Model 是该会话最近使用的模型名称或别名。
	Model string `json:"model,omitempty"`
	// Title 是会话摘要标题，通常来自首条消息或 provider 提供的标题。
	Title string `json:"title,omitempty"`
	// UpdatedAt 是该会话最近一次更新时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

type Session struct {
	// ID 是当前本地会话的唯一标识，前端和 WebSocket 都依赖它定位会话。
	ID string `json:"id"`
	// ProviderSessionID 是 provider 侧原始会话 ID；对 Codex 来说这里也用来保存 thread ID。
	ProviderSessionID string `json:"providerSessionId,omitempty"`
	// Provider 表示当前会话使用的后端提供方，例如 claude 或 codex。
	Provider string `json:"provider"`
	// Model 是当前会话传给 Claude CLI 的模型别名。
	Model string `json:"model"`
	// Workdir 是 Claude 在本轮会话里可访问的工作目录。
	Workdir string `json:"workdir"`
	// Messages 按时间顺序保存已经完成的用户消息和助手消息。
	Messages []*Message `json:"messages"`
	// Events 按时间顺序保存工具调用、步骤状态等过程事件。
	Events []*Event `json:"events"`
	// DraftMessage 保存流式输出中的助手草稿消息。
	DraftMessage *Message `json:"draftMessage,omitempty"`
	// IsRunning 表示当前会话是否仍有进行中的生成任务。
	IsRunning bool `json:"isRunning"`
	// UpdatedAt 记录该会话最后一次被服务端更新的时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

type serverEvent struct {
	// Type 表示服务端推送给前端的事件类型，例如 snapshot、message、task_status。
	Type string `json:"type"`
	// Session 在发送完整会话快照时携带当前会话内容。
	Session *Session `json:"session,omitempty"`
	// Message 在发送单条消息或流式增量时携带对应消息体。
	Message *Message `json:"message,omitempty"`
	// Log 在发送工具调用、步骤状态等事件时携带对应事件体。
	Log *Event `json:"log,omitempty"`
	// Running 表示当前会话是否仍处于生成中状态。
	Running bool `json:"running,omitempty"`
	// Error 在服务端处理失败时返回给前端展示错误信息。
	Error string `json:"error,omitempty"`
}

type clientEvent struct {
	// Type 表示前端发给服务端的事件类型，例如 hello、ping。
	Type string `json:"type"`
	// SessionID 是前端当前想连接或操作的会话 ID。
	SessionID string `json:"sessionId"`
	// Content 是前端事件附带的文本内容，当前主要用于发送消息场景。
	Content string `json:"content"`
}

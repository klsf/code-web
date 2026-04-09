package main

import (
	"encoding/json"
	"os"
	"strings"
)

const appConfigFile = "config.json"

type appConfig struct {
	AppName       string               `json:"appName"`
	Password      string               `json:"password"`
	Listen        string               `json:"listen"`
	Provider      string               `json:"provider"`
	PersistEvents bool                 `json:"persistEvents"`
	Providers     []*appProviderConfig `json:"providers"`
}

type appProviderConfig struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"defaultModel"`
	Available    bool     `json:"available"`
}

// loadAppConfig 读取应用配置；当配置文件不存在或无效时回退到内置默认值。
func loadAppConfig() *appConfig {
	cfg := defaultAppConfig()
	data, err := os.ReadFile(appConfigFile)
	if err != nil {
		return cfg
	}

	var loaded appConfig
	if err := json.Unmarshal(data, &loaded); err != nil {
		return cfg
	}

	if text := strings.TrimSpace(loaded.AppName); text != "" {
		cfg.AppName = text
	}
	if text := strings.TrimSpace(loaded.Password); text != "" {
		cfg.Password = text
	}
	if text := strings.TrimSpace(loaded.Listen); text != "" {
		cfg.Listen = normalizeListenAddr(text)
	}
	if text := strings.TrimSpace(loaded.Provider); text != "" {
		cfg.Provider = strings.ToLower(text)
	}
	cfg.PersistEvents = loaded.PersistEvents
	if len(loaded.Providers) > 0 {
		items := make([]*appProviderConfig, 0, len(loaded.Providers))
		for _, item := range loaded.Providers {
			if normalized := normalizeProviderConfig(item); normalized != nil {
				items = append(items, normalized)
			}
		}
		if len(items) > 0 {
			cfg.Providers = items
		}
	}

	cfg.Provider = cfg.defaultProviderID()
	return cfg
}

// defaultAppConfig 返回项目内置默认配置。
func defaultAppConfig() *appConfig {
	return &appConfig{
		AppName:       "AI Chat",
		Password:      "codex",
		Listen:        ":8080",
		Provider:      "claude",
		PersistEvents: false,
		Providers: []*appProviderConfig{
			{ID: "claude", Name: "Claude", Models: []string{"sonnet", "opus", "haiku"}, DefaultModel: "sonnet", Available: true},
			{ID: "codex", Name: "Codex", Models: []string{"gpt-5.4", "gpt-5.3-codex", "gpt-5.4-mini"}, DefaultModel: "gpt-5.4", Available: true},
		},
	}
}

func normalizeListenAddr(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ":8080"
	}
	if strings.HasPrefix(text, ":") {
		return text
	}
	if !strings.Contains(text, ":") {
		return ":" + text
	}
	return text
}

// normalizeProviderConfig 规范化单个 provider 配置。
func normalizeProviderConfig(item *appProviderConfig) *appProviderConfig {
	if item == nil {
		return nil
	}
	id := strings.ToLower(strings.TrimSpace(item.ID))
	if id == "" {
		return nil
	}
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = strings.ToUpper(id[:1]) + id[1:]
	}
	models := make([]string, 0, len(item.Models))
	for _, model := range item.Models {
		if text := strings.TrimSpace(model); text != "" {
			models = append(models, text)
		}
	}
	if len(models) == 0 {
		return nil
	}
	defaultModel := strings.TrimSpace(item.DefaultModel)
	if defaultModel == "" {
		defaultModel = models[0]
	}
	if !containsString(models, defaultModel) {
		models = append([]string{defaultModel}, models...)
	}
	return &appProviderConfig{
		ID:           id,
		Name:         name,
		Models:       models,
		DefaultModel: defaultModel,
		Available:    true,
	}
}

// defaultProviderID 返回默认 provider ID。
func (c *appConfig) defaultProviderID() string {
	if c == nil {
		return "claude"
	}
	if text := strings.ToLower(strings.TrimSpace(c.Provider)); text != "" {
		for _, item := range c.Providers {
			if item != nil && item.ID == text {
				return text
			}
		}
	}
	for _, item := range c.Providers {
		if item != nil && item.Available {
			return item.ID
		}
	}
	return "claude"
}

// providerModels 返回指定 provider 的模型列表。
func (c *appConfig) providerModels(providerID string) []string {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	for _, item := range c.Providers {
		if item != nil && item.ID == providerID {
			return append([]string(nil), item.Models...)
		}
	}
	return nil
}

// providerDefaultModel 返回指定 provider 的默认模型。
func (c *appConfig) providerDefaultModel(providerID string) string {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	for _, item := range c.Providers {
		if item != nil && item.ID == providerID {
			if text := strings.TrimSpace(item.DefaultModel); text != "" {
				return text
			}
			if len(item.Models) > 0 {
				return item.Models[0]
			}
			return ""
		}
	}
	return ""
}

// configuredDefaultModel 读取配置中的 provider 默认模型，不存在时回退到传入值。
func configuredDefaultModel(providerID, fallback string) string {
	if model := loadAppConfig().providerDefaultModel(providerID); strings.TrimSpace(model) != "" {
		return model
	}
	return strings.TrimSpace(fallback)
}

func containsString(items []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range items {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

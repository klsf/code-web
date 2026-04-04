package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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

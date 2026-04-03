package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const accountStatusCacheTTL = 5 * time.Second

func (s *sessionStore) readAccountStatus(ctx context.Context, providerID string) (*accountStatusData, error) {
	providerID = strings.TrimSpace(strings.ToLower(providerID))
	if providerID == "" {
		providerID = activeProvider.ID()
	}

	s.mu.RLock()
	cached, ok := s.accountStatus[providerID]
	s.mu.RUnlock()
	if ok && time.Since(cached.CheckedAt) < accountStatusCacheTTL {
		return cloneAccountStatus(cached.Data), nil
	}

	status, err := providerForID(providerID).ReadAccountStatus(ctx, s)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.accountStatus == nil {
		s.accountStatus = make(map[string]cachedAccountStatus)
	}
	s.accountStatus[providerID] = cachedAccountStatus{
		Data:      cloneAccountStatus(status),
		CheckedAt: time.Now(),
	}
	s.mu.Unlock()
	return cloneAccountStatus(status), nil
}

func cloneAccountStatus(status *accountStatusData) *accountStatusData {
	if status == nil {
		return nil
	}
	copyStatus := *status
	return &copyStatus
}

func readCommandOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text, err
		}
		return "", err
	}
	return text, nil
}

type jwtClaims struct {
	Email             string `json:"email"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Sub               string `json:"sub"`
}

func decodeJWTPayload(token string, target interface{}) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("empty token")
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return errors.New("invalid jwt")
	}
	payload := parts[1]
	if payload == "" {
		return errors.New("missing jwt payload")
	}
	return decodeBase64URLJSON(payload, target)
}

func decodeBase64URLJSON(raw string, target interface{}) error {
	padding := (4 - len(raw)%4) % 4
	if padding > 0 {
		raw += strings.Repeat("=", padding)
	}
	decoded, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, target)
}

func (p *codexProvider) ReadAccountStatus(ctx context.Context, _ *sessionStore) (*accountStatusData, error) {
	output, err := readCommandOutput(ctx, p.Executable(), "login", "status")
	if err != nil && strings.TrimSpace(output) == "" {
		return nil, err
	}
	if output == "" {
		return nil, errors.New("empty codex login status")
	}

	lower := strings.ToLower(output)
	status := &accountStatusData{
		LoggedIn: strings.Contains(lower, "logged in"),
		Detail:   output,
	}
	if idx := strings.Index(strings.ToLower(output), "logged in using "); idx >= 0 {
		status.Method = strings.TrimSpace(output[idx+len("logged in using "):])
	}

	authPath := strings.TrimSpace(os.Getenv("HOME"))
	if authPath != "" {
		authPath = filepath.Join(authPath, ".codex", "auth.json")
	}
	if authPath != "" {
		var authFile struct {
			Tokens struct {
				IDToken string `json:"id_token"`
			} `json:"tokens"`
		}
		if raw, readErr := os.ReadFile(authPath); readErr == nil && json.Unmarshal(raw, &authFile) == nil {
			var claims jwtClaims
			if decodeErr := decodeJWTPayload(authFile.Tokens.IDToken, &claims); decodeErr == nil {
				status.Identifier = strings.TrimSpace(claims.Email)
				status.Name = firstNonEmpty(strings.TrimSpace(claims.Name), strings.TrimSpace(claims.PreferredUsername))
				if status.Identifier == "" {
					status.Identifier = strings.TrimSpace(claims.Sub)
				}
			}
		}
	}
	return status, nil
}

type claudeAuthStatus struct {
	LoggedIn    bool   `json:"loggedIn"`
	AuthMethod  string `json:"authMethod"`
	APIProvider string `json:"apiProvider"`
}

func (p *claudeProvider) ReadAccountStatus(ctx context.Context, _ *sessionStore) (*accountStatusData, error) {
	output, err := readCommandOutput(ctx, p.Executable(), "auth", "status")
	if err != nil && strings.TrimSpace(output) == "" {
		return nil, err
	}
	if output == "" {
		return nil, errors.New("empty claude auth status")
	}

	var parsed claudeAuthStatus
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		return &accountStatusData{
			LoggedIn: strings.Contains(strings.ToLower(output), "loggedin\": true"),
			Detail:   output,
		}, nil
	}

	method := strings.TrimSpace(parsed.AuthMethod)
	detailParts := make([]string, 0, 2)
	if method != "" {
		detailParts = append(detailParts, method)
	}
	if provider := strings.TrimSpace(parsed.APIProvider); provider != "" {
		detailParts = append(detailParts, provider)
	}

	return &accountStatusData{
		LoggedIn: parsed.LoggedIn,
		Method:   method,
		Detail:   strings.Join(detailParts, " / "),
	}, nil
}

func accountSummary(status *accountStatusData) string {
	if status == nil {
		return "unknown"
	}

	parts := make([]string, 0, 2)
	if status.LoggedIn {
		parts = append(parts, "logged in")
	} else {
		parts = append(parts, "not logged in")
	}
	if status.Method != "" {
		parts = append(parts, status.Method)
	} else if status.Detail != "" {
		parts = append(parts, status.Detail)
	}
	if status.Identifier != "" {
		label := status.Identifier
		if status.Name != "" {
			label = status.Name + " <" + status.Identifier + ">"
		}
		parts = append(parts, label)
	} else if status.Name != "" {
		parts = append(parts, status.Name)
	}
	return fmt.Sprintf("%s", strings.Join(parts, " via "))
}

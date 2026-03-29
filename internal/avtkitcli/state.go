package avtkitcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	consolev1 "github.com/spatialwalk/open-platform-cli/api/generated/console/v1"
)

const authStateVersion = 1

var ErrNotLoggedIn = errors.New("not logged in")

type authStore struct {
	dir        string
	path       string
	legacyPath string
}

type authState struct {
	Version int        `json:"version"`
	BaseURL string     `json:"base_url"`
	User    userState  `json:"user"`
	Token   tokenState `json:"token"`
	SavedAt time.Time  `json:"saved_at"`
}

type userState struct {
	ID                  string `json:"id"`
	Email               string `json:"email,omitempty"`
	Username            string `json:"username,omitempty"`
	Nickname            string `json:"nickname,omitempty"`
	AvatarURL           string `json:"avatar_url,omitempty"`
	EmailVerified       bool   `json:"email_verified,omitempty"`
	CanCreateCharacters bool   `json:"can_create_characters,omitempty"`
}

type tokenState struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresIn    int32     `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

func newAuthStore(configDir string) (*authStore, error) {
	if strings.TrimSpace(configDir) == "" {
		userConfigDir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user config dir: %w", err)
		}
		configDir, legacyPath := defaultConfigPaths(userConfigDir)
		return &authStore{
			dir:        configDir,
			path:       filepath.Join(configDir, "auth.json"),
			legacyPath: legacyPath,
		}, nil
	}

	return &authStore{
		dir:  configDir,
		path: filepath.Join(configDir, "auth.json"),
	}, nil
}

func (s *authStore) Load() (*authState, error) {
	paths := []string{s.path}
	if s.legacyPath != "" && s.legacyPath != s.path {
		paths = append(paths, s.legacyPath)
	}

	var lastErr error
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			lastErr = fmt.Errorf("read auth state: %w", err)
			continue
		}

		var state authState
		if err := json.Unmarshal(payload, &state); err != nil {
			return nil, fmt.Errorf("decode auth state: %w", err)
		}
		if state.Version == 0 {
			state.Version = authStateVersion
		}
		return &state, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrNotLoggedIn
}

func (s *authStore) Save(state *authState) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	state.Version = authStateVersion
	state.SavedAt = time.Now().UTC()

	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auth state: %w", err)
	}

	tempPath := s.path + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o600); err != nil {
		return fmt.Errorf("write auth state: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("move auth state into place: %w", err)
	}
	if s.legacyPath != "" && s.legacyPath != s.path {
		_ = os.Remove(s.legacyPath)
	}
	return nil
}

func (s *authStore) Clear() error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if s.legacyPath != "" && s.legacyPath != s.path {
		if err := os.Remove(s.legacyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func defaultConfigPaths(userConfigDir string) (string, string) {
	configDir := filepath.Join(userConfigDir, cliName)
	legacyPath := filepath.Join(userConfigDir, "open-platform-cli", "auth.json")
	return configDir, legacyPath
}

func newAuthState(baseURL string, user *consolev1.ConsoleUser, token *consolev1.CLIAuthToken) *authState {
	return &authState{
		Version: authStateVersion,
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		User:    userStateFromProto(user),
		Token:   tokenStateFromProto(token),
		SavedAt: time.Now().UTC(),
	}
}

func userStateFromProto(user *consolev1.ConsoleUser) userState {
	if user == nil {
		return userState{}
	}
	return userState{
		ID:                  strings.TrimSpace(user.GetId()),
		Email:               strings.TrimSpace(user.GetEmail()),
		Username:            strings.TrimSpace(user.GetUsername()),
		Nickname:            strings.TrimSpace(user.GetNickname()),
		AvatarURL:           strings.TrimSpace(user.GetAvatarUrl()),
		EmailVerified:       user.GetEmailVerified(),
		CanCreateCharacters: user.GetCanCreateCharacters(),
	}
}

func tokenStateFromProto(token *consolev1.CLIAuthToken) tokenState {
	return tokenStateFromProtoWithFallback(token, "")
}

func tokenStateFromProtoWithFallback(token *consolev1.CLIAuthToken, refreshFallback string) tokenState {
	if token == nil {
		return tokenState{RefreshToken: strings.TrimSpace(refreshFallback)}
	}

	expiresAt := time.Time{}
	if ts := token.GetExpiresAt(); ts != nil {
		expiresAt = ts.AsTime().UTC()
	}
	if expiresAt.IsZero() && token.GetExpiresIn() > 0 {
		expiresAt = time.Now().UTC().Add(time.Duration(token.GetExpiresIn()) * time.Second)
	}

	refreshToken := strings.TrimSpace(token.GetRefreshToken())
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(refreshFallback)
	}

	return tokenState{
		AccessToken:  strings.TrimSpace(token.GetAccessToken()),
		RefreshToken: refreshToken,
		TokenType:    strings.TrimSpace(token.GetTokenType()),
		ExpiresIn:    token.GetExpiresIn(),
		ExpiresAt:    expiresAt,
	}
}

func (t tokenState) NeedsRefresh(now time.Time, skew time.Duration) bool {
	if strings.TrimSpace(t.AccessToken) == "" {
		return true
	}
	if t.ExpiresAt.IsZero() {
		return false
	}
	return !t.ExpiresAt.After(now.Add(skew))
}

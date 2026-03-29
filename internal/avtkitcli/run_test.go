package avtkitcli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	consolev1 "github.com/spatialwalk/open-platform-cli/api/generated/console/v1"
	consolev2 "github.com/spatialwalk/open-platform-cli/api/generated/console/v2"
	jsonapiv1 "github.com/spatialwalk/open-platform-cli/api/generated/jsonapi/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCallbackFromRequestSupportsCommonParamNames(t *testing.T) {
	req := httptest.NewRequest("GET", "/callback?auth_code=abc&authRequestId=req-1&state=xyz", nil)

	result := callbackFromRequest(req)

	if result.AuthCode != "abc" {
		t.Fatalf("expected auth code abc, got %q", result.AuthCode)
	}
	if result.AuthRequestID != "req-1" {
		t.Fatalf("expected auth request ID req-1, got %q", result.AuthRequestID)
	}
	if result.State != "xyz" {
		t.Fatalf("expected state xyz, got %q", result.State)
	}
}

func TestTokenStateFromProtoFallsBackToExistingRefreshToken(t *testing.T) {
	token := &consolev1.CLIAuthToken{
		AccessToken: "access",
		TokenType:   "Bearer",
		ExpiresAt:   timestamppb.New(time.Now().Add(time.Hour)),
	}

	state := tokenStateFromProtoWithFallback(token, "refresh")

	if state.RefreshToken != "refresh" {
		t.Fatalf("expected refresh token fallback, got %q", state.RefreshToken)
	}
}

func TestAuthStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := newAuthStore(dir)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}

	state := &authState{
		BaseURL: "https://console.spatialwalk.top",
		User: userState{
			ID:    "user-1",
			Email: "user@example.com",
		},
		Token: tokenState{
			AccessToken:  "access",
			RefreshToken: "refresh",
			ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		},
	}

	if err := store.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.BaseURL != state.BaseURL {
		t.Fatalf("expected base URL %q, got %q", state.BaseURL, loaded.BaseURL)
	}
	if loaded.User.Email != state.User.Email {
		t.Fatalf("expected email %q, got %q", state.User.Email, loaded.User.Email)
	}

	info, err := os.Stat(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("stat auth file: %v", err)
	}
	if perms := info.Mode().Perm(); perms != 0o600 {
		t.Fatalf("expected auth.json mode 0600, got %#o", perms)
	}
}

func TestAuthStoreLoadsLegacyConfigPath(t *testing.T) {
	root := t.TempDir()
	configDir, legacyPath := defaultConfigPaths(root)

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}

	legacyPayload := []byte(`{"version":1,"base_url":"https://console.spatialwalk.top","token":{"access_token":"access","refresh_token":"refresh"}}`)
	if err := os.WriteFile(legacyPath, legacyPayload, 0o600); err != nil {
		t.Fatalf("write legacy auth state: %v", err)
	}

	store := &authStore{
		dir:        configDir,
		path:       filepath.Join(configDir, "auth.json"),
		legacyPath: legacyPath,
	}

	state, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if state.BaseURL != "https://console.spatialwalk.top" {
		t.Fatalf("expected legacy base URL to load, got %q", state.BaseURL)
	}
}

func TestAuthStoreSaveRemovesLegacyConfigPath(t *testing.T) {
	root := t.TempDir()
	configDir, legacyPath := defaultConfigPaths(root)

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("write legacy auth state: %v", err)
	}

	store := &authStore{
		dir:        configDir,
		path:       filepath.Join(configDir, "auth.json"),
		legacyPath: legacyPath,
	}
	state := &authState{
		BaseURL: "https://console.spatialwalk.top",
		Token: tokenState{
			AccessToken:  "access",
			RefreshToken: "refresh",
		},
	}

	if err := store.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := os.Stat(store.path); err != nil {
		t.Fatalf("expected new auth state to exist: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy auth state to be removed, got %v", err)
	}
}

func TestRunUsageShowsAvtkitNames(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run(context.Background(), nil, Streams{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	var exitErr *ExitError
	if err == nil || !strings.Contains(stderr.String(), "avtkit [--version] [--base-url URL]") {
		t.Fatalf("expected avtkit usage in stderr, got err=%v stderr=%q", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "AVTKIT_CONSOLE_BASE_URL") {
		t.Fatalf("expected AVTKIT env usage, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "OP_CONSOLE_BASE_URL") || strings.Contains(stderr.String(), "OP_CONFIG_DIR") || strings.Contains(stderr.String(), "AVTKIT_REGION") {
		t.Fatalf("expected no OP_* env usage, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "auth status") {
		t.Fatalf("expected auth commands in usage, got %q", stderr.String())
	}
	for _, want := range []string{"--version", "app list|ls", "app create", "api-key list|ls", "api-key create", "avatar list|ls", "token create", "version"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected usage to contain %q, got %q", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "public-avatar") {
		t.Fatalf("expected usage to stop mentioning public-avatar, got %q", stderr.String())
	}
	if _, ok := err.(*ExitError); !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %#v", err)
	}
	if !strings.Contains(stderr.String(), "Create an app and automatically create an API key") {
		t.Fatalf("expected updated app create help text, got %q", stderr.String())
	}
}

func TestResolveOptionsUsesSingleDefaultBaseURL(t *testing.T) {
	resolved, err := resolveOptions(globalOptions{}, nil)
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}
	if resolved.BaseURL != defaultConsoleBaseURL {
		t.Fatalf("expected default base URL %q, got %q", defaultConsoleBaseURL, resolved.BaseURL)
	}
}

func TestResolveOptionsPrefersExplicitBaseURL(t *testing.T) {
	resolved, err := resolveOptions(globalOptions{BaseURL: "https://console-test.spatialwalk.top/"}, nil)
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}
	if resolved.BaseURL != "https://console-test.spatialwalk.top" {
		t.Fatalf("expected explicit base URL to be trimmed, got %q", resolved.BaseURL)
	}
}

func TestFirstQueryValue(t *testing.T) {
	values := url.Values{
		"empty": {""},
		"code":  {"abc"},
	}

	value := firstQueryValue(values, "missing", "empty", "code")
	if value != "abc" {
		t.Fatalf("expected abc, got %q", value)
	}
}

func TestWriteCallbackPageRendersStyledSuccessMarkup(t *testing.T) {
	recorder := httptest.NewRecorder()

	writeCallbackPage(recorder, http.StatusOK, callbackSuccessTitle, "You can return to the terminal.")

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML content type, got %q", got)
	}
	for _, want := range []string{
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`class="success"`,
		`AVTKit CLI`,
		`status-badge">Success</span>`,
		callbackSuccessTitle,
		`You can return to the terminal.`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got %q", want, body)
		}
	}
}

func TestWriteCallbackPageEscapesFailureContent(t *testing.T) {
	recorder := httptest.NewRecorder()
	title := `Authorization failed <script>alert("x")</script>`
	message := `callback <b>did not</b> include an authorization code`

	writeCallbackPage(recorder, http.StatusBadRequest, title, message)

	body := recorder.Body.String()
	for _, want := range []string{
		`class="failure"`,
		`status-badge">Failed</span>`,
		`Authorization failed &lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;`,
		`callback &lt;b&gt;did not&lt;/b&gt; include an authorization code`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got %q", want, body)
		}
	}
	if strings.Contains(body, `<script>alert("x")</script>`) || strings.Contains(body, `<b>did not</b>`) {
		t.Fatalf("expected HTML content to be escaped, got %q", body)
	}
}

func TestRunAppListUsesStoredAuthAndPrintsPagination(t *testing.T) {
	dir := t.TempDir()
	store, err := newAuthStore(dir)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer stored-access" {
			t.Fatalf("expected stored access token, got %q", got)
		}
		if got := r.URL.Query().Get("pagination.pageSize"); got != "2" {
			t.Fatalf("expected page size query of 2, got %q", got)
		}

		writeProtoJSON(t, w, &consolev1.AppServiceListAppsResponse{
			Apps: []*consolev1.App{
				{
					AppId:     "app_123",
					Name:      "demo-app",
					CreatedAt: timestamppb.New(time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)),
				},
			},
			Pagination: &jsonapiv1.PaginationResponse{},
		})
	}))
	defer server.Close()

	if err := store.Save(&authState{
		BaseURL: server.URL,
		Token: tokenState{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		},
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run(context.Background(), []string{"--config-dir", dir, "app", "ls", "--page-size", "2"}, Streams{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{"APP ID", "app_123", "demo-app"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, output)
		}
	}
}

func TestRunAppListRefreshesExpiredToken(t *testing.T) {
	dir := t.TempDir()
	store, err := newAuthStore(dir)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli/auth/token:refresh":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method %s", r.Method)
			}
			writeProtoJSON(t, w, &consolev1.RefreshCLIAuthTokenResponse{
				Token: &consolev1.CLIAuthToken{
					AccessToken:  "fresh-access",
					RefreshToken: "fresh-refresh",
					TokenType:    "Bearer",
					ExpiresAt:    timestamppb.New(time.Now().Add(time.Hour)),
				},
			})
		case "/v1/apps":
			if got := r.Header.Get("Authorization"); got != "Bearer fresh-access" {
				t.Fatalf("expected refreshed access token, got %q", got)
			}
			writeProtoJSON(t, w, &consolev1.AppServiceListAppsResponse{})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	if err := store.Save(&authState{
		BaseURL: server.URL,
		Token: tokenState{
			AccessToken:  "expired-access",
			RefreshToken: "stale-refresh",
			ExpiresAt:    time.Now().Add(-time.Minute).UTC(),
		},
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	err = Run(context.Background(), []string{"--config-dir", dir, "app", "list"}, Streams{
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	updated, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if updated.Token.AccessToken != "fresh-access" {
		t.Fatalf("expected refreshed access token, got %q", updated.Token.AccessToken)
	}
	if updated.Token.RefreshToken != "fresh-refresh" {
		t.Fatalf("expected refreshed refresh token, got %q", updated.Token.RefreshToken)
	}
}

func TestRunPublicAvatarListHidesCoverURLsByDefault(t *testing.T) {
	dir := t.TempDir()
	store, err := newAuthStore(dir)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}

	longCoverURL := "https://cdn.example.com/public/avatars/demo-avatar/assets/cover-images/final/avatar-cover-image-v20260329.png?signature=abcdefghijklmnopqrstuvwxyz"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/console/public-avatars" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer stored-access" {
			t.Fatalf("expected stored access token, got %q", got)
		}
		if got := r.URL.Query().Get("pagination.pageSize"); got != "2" {
			t.Fatalf("expected page size query of 2, got %q", got)
		}

		writeProtoJSON(t, w, &consolev2.ListPublicAvatarsResponse{
			PublicAvatars: []*consolev2.PublicAvatar{
				{
					Id:        "avatar_123",
					Name:      "Demo Avatar",
					CoverUrl:  longCoverURL,
					UpdatedAt: timestamppb.New(time.Date(2026, 3, 29, 14, 0, 0, 0, time.UTC)),
				},
			},
			Pagination: &jsonapiv1.PaginationResponse{
				NextPageToken: "2",
				TotalCount:    3,
			},
		})
	}))
	defer server.Close()

	if err := store.Save(&authState{
		BaseURL: server.URL,
		Token: tokenState{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		},
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run(context.Background(), []string{"--config-dir", dir, "avatar", "ls", "--page-size", "2"}, Streams{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{"AVATAR ID", "avatar_123", "Demo Avatar", "Next page token: 2", "Total count: 3"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, output)
		}
	}
	for _, unwanted := range []string{"COVER URL", longCoverURL, "Use --show-cover-urls to show full cover URLs."} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("expected output to omit %q by default, got %q", unwanted, output)
		}
	}
	if strings.Contains(output, "UPDATED AT") {
		t.Fatalf("expected output to omit UPDATED AT column, got %q", output)
	}
}

func TestRunPublicAvatarListShowCoverURLs(t *testing.T) {
	dir := t.TempDir()
	store, err := newAuthStore(dir)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}

	longCoverURL := "https://cdn.example.com/public/avatars/demo-avatar/assets/cover-images/final/avatar-cover-image-v20260329.png?signature=abcdefghijklmnopqrstuvwxyz"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/console/public-avatars" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		writeProtoJSON(t, w, &consolev2.ListPublicAvatarsResponse{
			PublicAvatars: []*consolev2.PublicAvatar{
				{
					Id:       "avatar_123",
					Name:     "Demo Avatar",
					CoverUrl: longCoverURL,
				},
			},
		})
	}))
	defer server.Close()

	if err := store.Save(&authState{
		BaseURL: server.URL,
		Token: tokenState{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		},
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run(context.Background(), []string{"--config-dir", dir, "avatar", "ls", "--show-cover-urls"}, Streams{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "COVER URL") {
		t.Fatalf("expected output to contain COVER URL header, got %q", output)
	}
	if !strings.Contains(output, longCoverURL) {
		t.Fatalf("expected output to contain full cover URL, got %q", output)
	}
}

func TestRunAPIKeyListMasksValuesByDefault(t *testing.T) {
	dir := t.TempDir()
	store, err := newAuthStore(dir)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/app_123/api-keys" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		writeProtoJSON(t, w, &consolev1.AppServiceListAPIKeysResponse{
			ApiKeys: []*consolev1.APIKey{
				{
					ApiKey:    "secretvalue123456789",
					CreatedAt: timestamppb.New(time.Date(2026, 3, 29, 13, 0, 0, 0, time.UTC)),
				},
			},
		})
	}))
	defer server.Close()

	if err := store.Save(&authState{
		BaseURL: server.URL,
		Token: tokenState{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		},
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	var stdout bytes.Buffer
	err = Run(context.Background(), []string{"--config-dir", dir, "api-key", "ls", "app_123"}, Streams{
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := stdout.String()
	if strings.Contains(output, "secretvalue123456789") {
		t.Fatalf("expected API key to be masked, got %q", output)
	}
	if !strings.Contains(output, "secretva******456789") {
		t.Fatalf("expected masked API key output, got %q", output)
	}
}

func TestRunSessionTokenCreateUsesAppAPIKey(t *testing.T) {
	dir := t.TempDir()
	store, err := newAuthStore(dir)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}

	var requestBody consolev1.CreateSessionTokenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/app_123":
			if got := r.Header.Get("Authorization"); got != "Bearer stored-access" {
				t.Fatalf("expected stored access token, got %q", got)
			}
			writeProtoJSON(t, w, &consolev1.AppServiceGetAppResponse{
				App: &consolev1.App{
					AppId: "app_123",
					ApiKeys: []*consolev1.APIKey{
						{ApiKey: "sk_live_1234567890"},
						{ApiKey: "sk_live_other_1234567890"},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/console/session-tokens":
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("expected no Authorization header, got %q", got)
			}
			if got := r.Header.Get("X-Api-Key"); got != "sk_live_1234567890" {
				t.Fatalf("expected selected API key, got %q", got)
			}

			payload, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			if err := protojson.Unmarshal(payload, &requestBody); err != nil {
				t.Fatalf("unmarshal request body: %v", err)
			}

			writeProtoJSON(t, w, &consolev1.CreateSessionTokenResponse{
				SessionToken: "session-token-123",
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	if err := store.Save(&authState{
		BaseURL: server.URL,
		Token: tokenState{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		},
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	start := time.Now().UTC()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run(context.Background(), []string{"--config-dir", dir, "token", "create", "--expire-in", "90m", "--model-version", "model-v1", "app_123"}, Streams{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v (stderr=%q)", err, stderr.String())
	}

	if requestBody.GetModelVersion() != "model-v1" {
		t.Fatalf("expected model version model-v1, got %q", requestBody.GetModelVersion())
	}
	expiresAt := time.Unix(requestBody.GetExpireAt(), 0).UTC()
	if expiresAt.Before(start.Add(89*time.Minute)) || expiresAt.After(start.Add(91*time.Minute)) {
		t.Fatalf("expected expireAt near 90m from now, got %s", expiresAt.Format(time.RFC3339))
	}

	output := stdout.String()
	for _, want := range []string{
		"Session token created.",
		"App ID: app_123",
		"API key: " + formatAPIKeyValue("sk_live_1234567890", false),
		"Model version: model-v1",
		"Session token: session-token-123",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, output)
		}
	}
	if !strings.Contains(stderr.String(), "multiple API keys found; using the first available key") {
		t.Fatalf("expected multiple API keys warning, got %q", stderr.String())
	}
}

func TestRunAppCreateAlsoCreatesAPIKey(t *testing.T) {
	dir := t.TempDir()
	store, err := newAuthStore(dir)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
			writeProtoJSON(t, w, &consolev1.AppServiceCreateAppResponse{
				AppId: "app_123",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/app_123/api-keys":
			writeProtoJSON(t, w, &consolev1.AppServiceCreateAPIKeyResponse{
				ApiKey: &consolev1.APIKey{
					ApiKey:    "sk_live_123",
					CreatedAt: timestamppb.New(time.Date(2026, 3, 29, 13, 0, 0, 0, time.UTC)),
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	if err := store.Save(&authState{
		BaseURL: server.URL,
		Token: tokenState{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		},
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run(context.Background(), []string{"--config-dir", dir, "app", "create", "demo-app"}, Streams{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"App created and API key generated.",
		"Name: demo-app",
		"App ID: app_123",
		"API key: sk_live_123",
		"Created at: 2026-03-29T13:00:00Z",
		"Store this API key securely.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunAppCreateReportsCreatedAppWhenAPIKeyCreationFails(t *testing.T) {
	dir := t.TempDir()
	store, err := newAuthStore(dir)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
			writeProtoJSON(t, w, &consolev1.AppServiceCreateAppResponse{
				AppId: "app_123",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/app_123/api-keys":
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	if err := store.Save(&authState{
		BaseURL: server.URL,
		Token: tokenState{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		},
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run(context.Background(), []string{"--config-dir", dir, "app", "create", "demo-app"}, Streams{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "app created with ID app_123, but failed to create API key") {
		t.Fatalf("expected partial failure error, got %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"App created.",
		"Name: demo-app",
		"App ID: app_123",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, output)
		}
	}
	if strings.Contains(output, "API key:") {
		t.Fatalf("did not expect API key output, got %q", output)
	}
	if !strings.Contains(stderr.String(), "avtkit api-key create app_123") {
		t.Fatalf("expected manual api-key create guidance, got %q", stderr.String())
	}
}

func TestRunAppDeleteRequiresYes(t *testing.T) {
	err := Run(context.Background(), []string{"app", "delete", "app_123"}, Streams{
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	})

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %d", exitErr.Code)
	}
	if !strings.Contains(exitErr.Error(), "--yes") {
		t.Fatalf("expected --yes guidance, got %q", exitErr.Error())
	}
}

func TestSelectAppAPIKeyPrefersRequestedKey(t *testing.T) {
	selected, notice, err := selectAppAPIKey("app_123", []*consolev1.APIKey{
		{ApiKey: "sk_live_first_123456"},
		{ApiKey: "sk_live_second_123456"},
	}, "sk_live_second_123456")
	if err != nil {
		t.Fatalf("selectAppAPIKey: %v", err)
	}
	if selected != "sk_live_second_123456" {
		t.Fatalf("expected requested API key, got %q", selected)
	}
	if notice != "" {
		t.Fatalf("expected empty notice when API key is explicitly selected, got %q", notice)
	}
}

func writeProtoJSON(t *testing.T, w http.ResponseWriter, message proto.Message) {
	t.Helper()

	payload, err := protojson.Marshal(message)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

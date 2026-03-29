package avtkitcli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	consolev1 "github.com/spatialwalk/open-platform-cli/api/generated/console/v1"
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
	if err == nil || !strings.Contains(stderr.String(), "avtkit [--base-url URL]") {
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
	for _, want := range []string{"app list", "app create", "api-key list", "api-key create"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected usage to contain %q, got %q", want, stderr.String())
		}
	}
	if _, ok := err.(*ExitError); !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %#v", err)
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
	err = Run(context.Background(), []string{"--config-dir", dir, "app", "list", "--page-size", "2"}, Streams{
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
	err = Run(context.Background(), []string{"--config-dir", dir, "api-key", "list", "app_123"}, Streams{
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

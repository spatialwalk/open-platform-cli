package avtkitcli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	consolev1 "github.com/spatialwalk/open-platform-cli/api/generated/console/v1"
)

const (
	defaultLoginTimeout   = 5 * time.Minute
	refreshSkew           = time.Minute
	defaultClientName     = "avtkit"
	defaultConsoleBaseURL = "https://api.openplatform.spatialwalk.cloud"
	callbackSuccessTitle  = "Authorization complete"
	callbackFailureTitle  = "Authorization failed"
	cliName               = "avtkit"
)

type Streams struct {
	Stdout io.Writer
	Stderr io.Writer
}

type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string {
	return e.Message
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Code > 0 {
		return exitErr.Code
	}
	return 1
}

type globalOptions struct {
	BaseURL   string
	ConfigDir string
}

type resolvedOptions struct {
	BaseURL string
}

type loginOptions struct {
	NoBrowser  bool
	Timeout    time.Duration
	ClientName string
}

type statusOptions struct {
	Offline bool
}

type callbackResult struct {
	AuthCode      string
	AuthRequestID string
	State         string
	ErrCode       string
	ErrDesc       string
}

func Run(ctx context.Context, args []string, streams Streams) error {
	if streams.Stdout == nil {
		streams.Stdout = io.Discard
	}
	if streams.Stderr == nil {
		streams.Stderr = io.Discard
	}

	store, err := newAuthStore("")
	if err != nil {
		return err
	}

	app := &app{
		streams: streams,
		store:   store,
	}
	return app.run(ctx, args)
}

type app struct {
	streams Streams
	store   *authStore
}

func (a *app) run(ctx context.Context, args []string) error {
	global := globalOptions{
		BaseURL:   strings.TrimSpace(os.Getenv("AVTKIT_CONSOLE_BASE_URL")),
		ConfigDir: strings.TrimSpace(os.Getenv("AVTKIT_CONFIG_DIR")),
	}

	fs := flag.NewFlagSet(cliName, flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.StringVar(&global.BaseURL, "base-url", global.BaseURL, "Console API base URL")
	fs.StringVar(&global.ConfigDir, "config-dir", global.ConfigDir, "Override CLI config directory")
	fs.Usage = func() {
		fmt.Fprintln(a.streams.Stderr, "Usage:")
		fmt.Fprintf(a.streams.Stderr, "  %s [--base-url URL] [--config-dir DIR] <command>\n", cliName)
		fmt.Fprintln(a.streams.Stderr)
		fmt.Fprintln(a.streams.Stderr, "Commands:")
		fmt.Fprintln(a.streams.Stderr, "  login            Sign in with the browser-based CLI auth flow")
		fmt.Fprintln(a.streams.Stderr, "  logout           Revoke stored tokens and clear local auth state")
		fmt.Fprintln(a.streams.Stderr, "  auth status      Show the current login state")
		fmt.Fprintln(a.streams.Stderr, "  auth refresh     Refresh the stored access token")
		fmt.Fprintln(a.streams.Stderr, "  app list         List apps for the current account")
		fmt.Fprintln(a.streams.Stderr, "  app create       Create an app and automatically create an API key")
		fmt.Fprintln(a.streams.Stderr, "  app get          Show app details and API keys")
		fmt.Fprintln(a.streams.Stderr, "  app delete       Delete an app")
		fmt.Fprintln(a.streams.Stderr, "  api-key list     List API keys for an app")
		fmt.Fprintln(a.streams.Stderr, "  api-key create   Create an API key for an app")
		fmt.Fprintln(a.streams.Stderr, "  api-key delete   Delete an API key from an app")
		fmt.Fprintln(a.streams.Stderr, "  public-avatar list   List public avatars")
		fmt.Fprintln(a.streams.Stderr, "  session-token create Create a temporary session token for an app")
		fmt.Fprintln(a.streams.Stderr)
		fmt.Fprintln(a.streams.Stderr, "Environment:")
		fmt.Fprintln(a.streams.Stderr, "  AVTKIT_CONSOLE_BASE_URL overrides the API base URL")
		fmt.Fprintln(a.streams.Stderr, "  AVTKIT_CONFIG_DIR overrides the config directory")
	}

	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}

	if global.ConfigDir != "" {
		store, err := newAuthStore(global.ConfigDir)
		if err != nil {
			return err
		}
		a.store = store
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return &ExitError{Code: 2}
	}

	switch rest[0] {
	case "login":
		return a.runLogin(ctx, global, rest[1:])
	case "logout":
		return a.runLogout(ctx, global, rest[1:])
	case "auth":
		return a.runAuth(ctx, global, rest[1:])
	case "app":
		return a.runApp(ctx, global, rest[1:])
	case "api-key":
		return a.runAPIKey(ctx, global, rest[1:])
	case "public-avatar":
		return a.runPublicAvatar(ctx, global, rest[1:])
	case "session-token":
		return a.runSessionToken(ctx, global, rest[1:])
	case "help", "-h", "--help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown command %q", rest[0])}
	}
}

func (a *app) runAuth(ctx context.Context, global globalOptions, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s auth <status|refresh>\n", cliName)
		return &ExitError{Code: 2}
	}

	switch args[0] {
	case "status":
		return a.runStatus(ctx, global, args[1:])
	case "refresh":
		return a.runRefresh(ctx, global, args[1:])
	case "help", "-h", "--help":
		fmt.Fprintf(a.streams.Stderr, "Usage: %s auth <status|refresh>\n", cliName)
		return nil
	default:
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown auth command %q", args[0])}
	}
}

func (a *app) runLogin(ctx context.Context, global globalOptions, args []string) error {
	opts := loginOptions{
		Timeout:    defaultLoginTimeout,
		ClientName: defaultClientName,
	}

	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.BoolVar(&opts.NoBrowser, "no-browser", false, "Print the authorization URL instead of opening a browser")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Maximum time to wait for browser approval")
	fs.StringVar(&opts.ClientName, "client-name", opts.ClientName, "Display name shown on the browser approval page")
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 0 {
		return &ExitError{Code: 2, Message: "login does not accept positional arguments"}
	}
	if opts.Timeout <= 0 {
		return &ExitError{Code: 2, Message: "--timeout must be greater than zero"}
	}

	resolved, err := resolveOptions(global, nil)
	if err != nil {
		return err
	}

	client := NewAPIClient(resolved.BaseURL)
	state, err := a.login(ctx, client, resolved, opts)
	if err != nil {
		return err
	}
	if err := a.store.Save(state); err != nil {
		return fmt.Errorf("save auth state: %w", err)
	}

	fmt.Fprintf(a.streams.Stdout, "Logged in to %s\n", state.BaseURL)
	if state.User.Email != "" {
		fmt.Fprintf(a.streams.Stdout, "User: %s\n", state.User.Email)
	} else {
		fmt.Fprintf(a.streams.Stdout, "User ID: %s\n", state.User.ID)
	}
	if !state.Token.ExpiresAt.IsZero() {
		fmt.Fprintf(a.streams.Stdout, "Access token expires at: %s\n", state.Token.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}

func (a *app) runStatus(ctx context.Context, global globalOptions, args []string) error {
	opts := statusOptions{}
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.BoolVar(&opts.Offline, "offline", false, "Only show stored state without calling the API")
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 0 {
		return &ExitError{Code: 2, Message: "status does not accept positional arguments"}
	}

	state, err := a.store.Load()
	if err != nil {
		if errors.Is(err, ErrNotLoggedIn) {
			fmt.Fprintln(a.streams.Stdout, "Status: logged out")
			return nil
		}
		return err
	}

	resolved, err := resolveOptions(global, state)
	if err != nil {
		return err
	}
	state.BaseURL = resolved.BaseURL

	client := NewAPIClient(resolved.BaseURL)
	if !opts.Offline {
		if state, err = a.syncStatus(ctx, client, state); err != nil {
			return err
		}
		if err := a.store.Save(state); err != nil {
			return fmt.Errorf("save auth state: %w", err)
		}
	}

	a.printStatus(state, opts.Offline)
	return nil
}

func (a *app) runRefresh(ctx context.Context, global globalOptions, args []string) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 0 {
		return &ExitError{Code: 2, Message: "refresh does not accept positional arguments"}
	}

	state, err := a.store.Load()
	if err != nil {
		if errors.Is(err, ErrNotLoggedIn) {
			return &ExitError{Code: 1, Message: "not logged in"}
		}
		return err
	}

	resolved, err := resolveOptions(global, state)
	if err != nil {
		return err
	}
	state.BaseURL = resolved.BaseURL

	client := NewAPIClient(resolved.BaseURL)
	state, err = a.refreshState(ctx, client, state)
	if err != nil {
		return err
	}
	if err := a.store.Save(state); err != nil {
		return fmt.Errorf("save auth state: %w", err)
	}

	fmt.Fprintln(a.streams.Stdout, "Access token refreshed.")
	if !state.Token.ExpiresAt.IsZero() {
		fmt.Fprintf(a.streams.Stdout, "Access token expires at: %s\n", state.Token.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}

func (a *app) runLogout(ctx context.Context, global globalOptions, args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 0 {
		return &ExitError{Code: 2, Message: "logout does not accept positional arguments"}
	}

	state, err := a.store.Load()
	if err != nil {
		if errors.Is(err, ErrNotLoggedIn) {
			fmt.Fprintln(a.streams.Stdout, "Already logged out.")
			return nil
		}
		return err
	}

	resolved, err := resolveOptions(global, state)
	if err != nil {
		return err
	}
	state.BaseURL = resolved.BaseURL

	client := NewAPIClient(resolved.BaseURL)
	if err := a.revokeTokens(ctx, client, state); err != nil {
		fmt.Fprintf(a.streams.Stderr, "warning: failed to revoke remote tokens: %v\n", err)
	}
	if err := a.store.Clear(); err != nil {
		return fmt.Errorf("clear auth state: %w", err)
	}

	fmt.Fprintln(a.streams.Stdout, "Logged out.")
	return nil
}

func (a *app) login(ctx context.Context, client *APIClient, resolved resolvedOptions, opts loginOptions) (*authState, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start local callback listener: %w", err)
	}
	defer listener.Close()

	redirectURL := &url.URL{
		Scheme: "http",
		Host:   listener.Addr().String(),
		Path:   "/callback",
	}

	codeVerifier, codeChallenge, err := newPKCEVerifier()
	if err != nil {
		return nil, err
	}
	stateValue, err := randomToken(18)
	if err != nil {
		return nil, err
	}

	sessionResp, err := client.CreateCLIAuthSession(ctx, &consolev1.CreateCLIAuthSessionRequest{
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: consolev1.CLIAuthCodeChallengeMethod_CLI_AUTH_CODE_CHALLENGE_METHOD_S256,
		RedirectUri:         redirectURL.String(),
		State:               stringPtr(stateValue),
		ClientName:          stringPtr(strings.TrimSpace(opts.ClientName)),
	})
	if err != nil {
		return nil, err
	}
	if sessionResp.GetAuthorizeUrl() == "" {
		return nil, errors.New("create CLI auth session returned an empty authorize URL")
	}

	waitTimeout := opts.Timeout
	if expiresAt := sessionResp.GetExpiresAt(); expiresAt != nil {
		expiresAtTime := expiresAt.AsTime()
		if !expiresAtTime.IsZero() {
			remaining := time.Until(expiresAtTime)
			if remaining > 0 && remaining < waitTimeout {
				waitTimeout = remaining
			}
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()

	callbackCh, shutdownServer, err := startCallbackServer(listener, sessionResp.GetAuthRequestId(), stateValue)
	if err != nil {
		return nil, err
	}
	defer shutdownServer(context.Background())

	fmt.Fprintf(a.streams.Stdout, "Authorize this CLI session in your browser:\n%s\n", sessionResp.GetAuthorizeUrl())
	if !opts.NoBrowser {
		if err := openBrowser(waitCtx, sessionResp.GetAuthorizeUrl()); err != nil {
			fmt.Fprintf(a.streams.Stderr, "warning: could not open browser automatically: %v\n", err)
		}
	}

	var callback callbackResult
	select {
	case result := <-callbackCh:
		callback = result
	case <-waitCtx.Done():
		return nil, fmt.Errorf("waiting for browser authorization: %w", waitCtx.Err())
	}

	if callback.ErrCode != "" {
		if callback.ErrDesc != "" {
			return nil, fmt.Errorf("authorization failed: %s (%s)", callback.ErrCode, callback.ErrDesc)
		}
		return nil, fmt.Errorf("authorization failed: %s", callback.ErrCode)
	}

	authRequestID := sessionResp.GetAuthRequestId()
	if callback.AuthRequestID != "" {
		authRequestID = callback.AuthRequestID
	}

	exchangeResp, err := client.ExchangeCLIAuthToken(waitCtx, &consolev1.ExchangeCLIAuthTokenRequest{
		AuthRequestId: authRequestID,
		AuthCode:      callback.AuthCode,
		CodeVerifier:  codeVerifier,
	})
	if err != nil {
		return nil, err
	}
	if exchangeResp.GetToken() == nil {
		return nil, errors.New("exchange CLI auth token returned no token")
	}

	state := newAuthState(resolved.BaseURL, exchangeResp.GetUser(), exchangeResp.GetToken())
	if state.User.ID == "" {
		user, err := client.GetMe(waitCtx, state.Token.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("fetch current user after login: %w", err)
		}
		state.User = userStateFromProto(user)
	}
	return state, nil
}

func (a *app) syncStatus(ctx context.Context, client *APIClient, state *authState) (*authState, error) {
	refreshed, err := a.ensureFreshToken(ctx, client, state)
	if err != nil {
		return nil, err
	}
	state = refreshed

	user, err := client.GetMe(ctx, state.Token.AccessToken)
	if err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusUnauthorized {
			state, err = a.refreshState(ctx, client, state)
			if err != nil {
				return nil, err
			}
			user, err = client.GetMe(ctx, state.Token.AccessToken)
		}
	}
	if err != nil {
		return nil, err
	}

	state.User = userStateFromProto(user)
	state.SavedAt = time.Now().UTC()
	return state, nil
}

func (a *app) ensureFreshToken(ctx context.Context, client *APIClient, state *authState) (*authState, error) {
	if !state.Token.NeedsRefresh(time.Now().UTC(), refreshSkew) {
		return state, nil
	}
	return a.refreshState(ctx, client, state)
}

func (a *app) refreshState(ctx context.Context, client *APIClient, state *authState) (*authState, error) {
	if strings.TrimSpace(state.Token.RefreshToken) == "" {
		return nil, errors.New("stored auth state does not contain a refresh token")
	}

	currentRefreshToken := state.Token.RefreshToken
	resp, err := client.RefreshCLIAuthToken(ctx, &consolev1.RefreshCLIAuthTokenRequest{
		RefreshToken: currentRefreshToken,
	})
	if err != nil {
		return nil, err
	}
	if resp.GetToken() == nil {
		return nil, errors.New("refresh CLI auth token returned no token")
	}

	state.Token = tokenStateFromProtoWithFallback(resp.GetToken(), currentRefreshToken)
	state.SavedAt = time.Now().UTC()
	return state, nil
}

func (a *app) revokeTokens(ctx context.Context, client *APIClient, state *authState) error {
	if strings.TrimSpace(state.Token.RefreshToken) == "" {
		return nil
	}

	req := &consolev1.RevokeCLIAuthTokenRequest{
		RefreshToken: state.Token.RefreshToken,
	}
	if accessToken := strings.TrimSpace(state.Token.AccessToken); accessToken != "" {
		req.AccessToken = stringPtr(accessToken)
	}
	return client.RevokeCLIAuthToken(ctx, req)
}

func (a *app) printStatus(state *authState, offline bool) {
	fmt.Fprintln(a.streams.Stdout, "Status: logged in")
	fmt.Fprintf(a.streams.Stdout, "Base URL: %s\n", state.BaseURL)
	if state.User.ID != "" {
		fmt.Fprintf(a.streams.Stdout, "User ID: %s\n", state.User.ID)
	}
	if state.User.Email != "" {
		fmt.Fprintf(a.streams.Stdout, "Email: %s\n", state.User.Email)
	}
	if state.User.Username != "" {
		fmt.Fprintf(a.streams.Stdout, "Username: %s\n", state.User.Username)
	}
	if state.User.Nickname != "" {
		fmt.Fprintf(a.streams.Stdout, "Nickname: %s\n", state.User.Nickname)
	}
	if state.Token.TokenType != "" {
		fmt.Fprintf(a.streams.Stdout, "Token type: %s\n", state.Token.TokenType)
	}
	if !state.Token.ExpiresAt.IsZero() {
		label := "Access token expires at"
		if offline {
			label = "Stored access token expires at"
		}
		fmt.Fprintf(a.streams.Stdout, "%s: %s\n", label, state.Token.ExpiresAt.Format(time.RFC3339))
	}
	if !state.SavedAt.IsZero() {
		fmt.Fprintf(a.streams.Stdout, "State saved at: %s\n", state.SavedAt.Format(time.RFC3339))
	}
}

func resolveOptions(global globalOptions, state *authState) (resolvedOptions, error) {
	baseURL := strings.TrimSpace(global.BaseURL)
	if baseURL == "" && state != nil {
		baseURL = strings.TrimSpace(state.BaseURL)
	}
	if baseURL == "" {
		baseURL = defaultConsoleBaseURL
	}

	return resolvedOptions{
		BaseURL: strings.TrimRight(baseURL, "/"),
	}, nil
}

func newPKCEVerifier() (string, string, error) {
	verifier, err := randomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("generate PKCE verifier: %w", err)
	}

	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func openBrowser(ctx context.Context, target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", target)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", target)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func startCallbackServer(listener net.Listener, expectedAuthRequestID, expectedState string) (<-chan callbackResult, func(context.Context) error, error) {
	resultCh := make(chan callbackResult, 1)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			result := callbackFromRequest(r)
			if result.ErrCode == "" {
				if expectedState != "" && result.State != "" && result.State != expectedState {
					result = callbackResult{
						ErrCode: "invalid_state",
						ErrDesc: "returned state does not match the pending login request",
					}
				}
				if expectedAuthRequestID != "" && result.AuthRequestID != "" && result.AuthRequestID != expectedAuthRequestID {
					result = callbackResult{
						ErrCode: "invalid_request",
						ErrDesc: "returned auth request ID does not match the pending login request",
					}
				}
				if result.AuthCode == "" {
					result = callbackResult{
						ErrCode: "missing_code",
						ErrDesc: "callback did not include an authorization code",
					}
				}
			}

			if result.ErrCode != "" {
				writeCallbackPage(w, http.StatusBadRequest, callbackFailureTitle, result.ErrDesc)
			} else {
				writeCallbackPage(w, http.StatusOK, callbackSuccessTitle, "You can return to the terminal.")
			}

			select {
			case resultCh <- result:
			default:
			}
		}),
	}

	go func() {
		_ = server.Serve(listener)
	}()

	return resultCh, server.Shutdown, nil
}

func callbackFromRequest(r *http.Request) callbackResult {
	query := r.URL.Query()
	result := callbackResult{
		AuthCode:      firstQueryValue(query, "auth_code", "authCode", "code"),
		AuthRequestID: firstQueryValue(query, "auth_request_id", "authRequestId", "request_id", "requestId"),
		State:         firstQueryValue(query, "state"),
		ErrCode:       firstQueryValue(query, "error"),
		ErrDesc:       firstQueryValue(query, "error_description", "errorDescription", "message"),
	}
	return result
}

func firstQueryValue(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func writeCallbackPage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	pageTone := "success"
	statusLabel := "Success"
	statusIcon := "&#10003;"
	if status >= http.StatusBadRequest {
		pageTone = "failure"
		statusLabel = "Failed"
		statusIcon = "!"
	}

	_, _ = fmt.Fprintf(
		w,
		`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root{color-scheme:light;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;padding:24px;display:flex;align-items:center;justify-content:center;background:radial-gradient(circle at top,rgba(59,130,246,.16),transparent 34%%),linear-gradient(180deg,#f8fafc 0%%,#eef2f7 100%%);color:#0f172a}
.shell{width:min(100%%,560px)}
.card{position:relative;overflow:hidden;border:1px solid rgba(15,23,42,.1);border-radius:24px;background:rgba(255,255,255,.94);box-shadow:0 24px 60px rgba(15,23,42,.12);backdrop-filter:blur(12px);padding:28px}
.card:before{content:"";position:absolute;top:0;left:0;right:0;height:4px;background:linear-gradient(90deg,var(--accent-soft),var(--accent))}
.badge-row{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;margin-bottom:22px}
.brand-badge,.status-badge{display:inline-flex;align-items:center;min-height:32px;padding:0 12px;border-radius:999px;border:1px solid rgba(15,23,42,.08);font-size:12px;font-weight:600;letter-spacing:.08em;text-transform:uppercase}
.brand-badge{background:#fff;color:#475569}
.status-badge{background:var(--accent-bg);border-color:var(--accent-border);color:var(--accent)}
.status-icon{width:60px;height:60px;border-radius:18px;display:flex;align-items:center;justify-content:center;margin-bottom:20px;background:var(--accent-bg);border:1px solid var(--accent-border);color:var(--accent);font-size:30px;font-weight:700;line-height:1}
h1{margin:0 0 12px;font-size:clamp(1.75rem,4vw,2.25rem);line-height:1.1;letter-spacing:-.03em}
p{margin:0;font-size:1rem;line-height:1.7;color:#475569}
.success{--accent:#0f766e;--accent-soft:rgba(45,212,191,.4);--accent-bg:#ecfdf5;--accent-border:rgba(15,118,110,.16)}
.failure{--accent:#b42318;--accent-soft:rgba(248,113,113,.4);--accent-bg:#fef2f2;--accent-border:rgba(180,35,24,.16)}
@media (max-width:640px){
body{padding:16px}
.card{padding:22px 20px;border-radius:20px}
.status-icon{width:52px;height:52px;border-radius:16px;font-size:26px}
}
</style>
</head>
<body class="%s">
<main class="shell">
<section class="card" role="status" aria-live="polite">
<div class="badge-row">
<span class="brand-badge">AVTKit CLI</span>
<span class="status-badge">%s</span>
</div>
<div class="status-icon" aria-hidden="true">%s</div>
<h1>%s</h1>
<p>%s</p>
</section>
</main>
</body>
</html>`,
		html.EscapeString(title),
		pageTone,
		statusLabel,
		statusIcon,
		html.EscapeString(title),
		html.EscapeString(message),
	)
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	return &value
}

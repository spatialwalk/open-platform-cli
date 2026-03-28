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
	defaultLoginTimeout  = 5 * time.Minute
	refreshSkew          = time.Minute
	defaultClientName    = "avtkit"
	callbackSuccessTitle = "Authorization complete"
	callbackFailureTitle = "Authorization failed"
	cliName              = "avtkit"
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
	Region    string
	ConfigDir string
}

type resolvedOptions struct {
	BaseURL string
	Region  string
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
		Region:    strings.TrimSpace(os.Getenv("AVTKIT_REGION")),
		ConfigDir: strings.TrimSpace(os.Getenv("AVTKIT_CONFIG_DIR")),
	}

	fs := flag.NewFlagSet(cliName, flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.StringVar(&global.BaseURL, "base-url", global.BaseURL, "Console API base URL")
	fs.StringVar(&global.Region, "region", global.Region, "Region to use (cn, us, test, dev)")
	fs.StringVar(&global.ConfigDir, "config-dir", global.ConfigDir, "Override CLI config directory")
	fs.Usage = func() {
		fmt.Fprintln(a.streams.Stderr, "Usage:")
		fmt.Fprintf(a.streams.Stderr, "  %s [--base-url URL] [--region NAME] [--config-dir DIR] <command>\n", cliName)
		fmt.Fprintln(a.streams.Stderr)
		fmt.Fprintln(a.streams.Stderr, "Commands:")
		fmt.Fprintln(a.streams.Stderr, "  login            Sign in with the browser-based CLI auth flow")
		fmt.Fprintln(a.streams.Stderr, "  logout           Revoke stored tokens and clear local auth state")
		fmt.Fprintln(a.streams.Stderr, "  auth status      Show the current login state")
		fmt.Fprintln(a.streams.Stderr, "  auth refresh     Refresh the stored access token")
		fmt.Fprintln(a.streams.Stderr)
		fmt.Fprintln(a.streams.Stderr, "Environment:")
		fmt.Fprintln(a.streams.Stderr, "  AVTKIT_CONSOLE_BASE_URL overrides the API base URL")
		fmt.Fprintln(a.streams.Stderr, "  AVTKIT_REGION selects the default region")
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
	state.Region = resolved.Region

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
	state.Region = resolved.Region

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
	state.Region = resolved.Region

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

	state := newAuthState(resolved.BaseURL, resolved.Region, exchangeResp.GetUser(), exchangeResp.GetToken())
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
	if state.Region != "" {
		fmt.Fprintf(a.streams.Stdout, "Region: %s\n", state.Region)
	}
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
	region := strings.TrimSpace(global.Region)
	if region == "" && state != nil {
		region = state.Region
	}
	if region == "" {
		region = "cn"
	}

	baseURL := strings.TrimSpace(global.BaseURL)
	if baseURL == "" && state != nil {
		baseURL = strings.TrimSpace(state.BaseURL)
	}
	if baseURL == "" {
		baseURL = defaultBaseURLForRegion(region)
	}
	if baseURL == "" {
		return resolvedOptions{}, &ExitError{
			Code:    2,
			Message: fmt.Sprintf("unsupported region %q; pass --base-url or choose one of cn, us, test, dev", region),
		}
	}

	return resolvedOptions{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Region:  region,
	}, nil
}

func defaultBaseURLForRegion(region string) string {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "cn":
		return "https://console.spatialwalk.top"
	case "us":
		return "https://api.openplatform.spatialwalk.cloud"
	case "test":
		return "https://console-test.spatialwalk.top"
	case "dev":
		return "http://localhost:8083"
	default:
		return ""
	}
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
	_, _ = fmt.Fprintf(
		w,
		"<!doctype html><html><head><meta charset=\"utf-8\"><title>%s</title></head><body><h1>%s</h1><p>%s</p></body></html>",
		html.EscapeString(title),
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

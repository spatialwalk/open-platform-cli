package avtkitcli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	consolev1 "github.com/spatialwalk/open-platform-cli/api/generated/console/v1"
	jsonapiv1 "github.com/spatialwalk/open-platform-cli/api/generated/jsonapi/v1"
)

const defaultListPageSize = 20

type appCreateOptions struct {
	Name string
}

type listOptions struct {
	PageSize  int
	PageToken string
}

type appGetOptions struct {
	ShowValues bool
}

type deleteOptions struct {
	Yes bool
}

type apiKeyListOptions struct {
	listOptions
	ShowValues bool
}

type authedSession struct {
	client *APIClient
	state  *authState
}

func (a *app) runApp(ctx context.Context, global globalOptions, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s app <list|create|get|delete>\n", cliName)
		return &ExitError{Code: 2}
	}

	switch args[0] {
	case "list":
		return a.runAppList(ctx, global, args[1:])
	case "create":
		return a.runAppCreate(ctx, global, args[1:])
	case "get":
		return a.runAppGet(ctx, global, args[1:])
	case "delete":
		return a.runAppDelete(ctx, global, args[1:])
	case "help", "-h", "--help":
		fmt.Fprintf(a.streams.Stderr, "Usage: %s app <list|create|get|delete>\n", cliName)
		return nil
	default:
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown app command %q", args[0])}
	}
}

func (a *app) runAPIKey(ctx context.Context, global globalOptions, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s api-key <list|create|delete>\n", cliName)
		return &ExitError{Code: 2}
	}

	switch args[0] {
	case "list":
		return a.runAPIKeyList(ctx, global, args[1:])
	case "create":
		return a.runAPIKeyCreate(ctx, global, args[1:])
	case "delete":
		return a.runAPIKeyDelete(ctx, global, args[1:])
	case "help", "-h", "--help":
		fmt.Fprintf(a.streams.Stderr, "Usage: %s api-key <list|create|delete>\n", cliName)
		return nil
	default:
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown api-key command %q", args[0])}
	}
}

func (a *app) runAppList(ctx context.Context, global globalOptions, args []string) error {
	opts := listOptions{PageSize: defaultListPageSize}

	fs := flag.NewFlagSet("app list", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.IntVar(&opts.PageSize, "page-size", opts.PageSize, "Number of apps to fetch")
	fs.StringVar(&opts.PageToken, "page-token", "", "Pagination token returned by a previous list command")
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s app list [--page-size N] [--page-token TOKEN]\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 0 {
		return &ExitError{Code: 2, Message: "app list does not accept positional arguments"}
	}
	if opts.PageSize <= 0 {
		return &ExitError{Code: 2, Message: "--page-size must be greater than zero"}
	}

	var resp *consolev1.AppServiceListAppsResponse
	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		var err error
		resp, err = session.client.ListApps(ctx, session.state.Token.AccessToken, &consolev1.AppServiceListAppsRequest{
			Pagination: &jsonapiv1.PaginationRequest{
				PageSize:  int32(opts.PageSize),
				PageToken: strings.TrimSpace(opts.PageToken),
			},
		})
		return err
	}); err != nil {
		return err
	}

	apps := resp.GetApps()
	if len(apps) == 0 {
		fmt.Fprintln(a.streams.Stdout, "No apps found.")
		return nil
	}

	tw := tabwriter.NewWriter(a.streams.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "APP ID\tNAME\tAPI KEYS\tCREATED AT")
	for _, item := range apps {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n",
			item.GetAppId(),
			defaultIfEmpty(item.GetName(), "-"),
			len(item.GetApiKeys()),
			formatProtoTimestamp(item.GetCreatedAt()),
		)
	}
	_ = tw.Flush()

	printPagination(a.streams.Stdout, resp.GetPagination())
	return nil
}

func (a *app) runAppCreate(ctx context.Context, global globalOptions, args []string) error {
	opts := appCreateOptions{}

	fs := flag.NewFlagSet("app create", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.StringVar(&opts.Name, "name", "", "Application name")
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s app create [--name NAME] [NAME]\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}

	positional := fs.Args()
	switch {
	case strings.TrimSpace(opts.Name) != "" && len(positional) > 0:
		return &ExitError{Code: 2, Message: "app create accepts either --name or a single positional NAME"}
	case strings.TrimSpace(opts.Name) == "" && len(positional) == 1:
		opts.Name = positional[0]
	case strings.TrimSpace(opts.Name) == "" && len(positional) == 0:
		return &ExitError{Code: 2, Message: "app name is required"}
	case len(positional) > 1:
		return &ExitError{Code: 2, Message: "app create accepts at most one positional NAME"}
	}

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return &ExitError{Code: 2, Message: "app name is required"}
	}

	var resp *consolev1.AppServiceCreateAppResponse
	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		var err error
		resp, err = session.client.CreateApp(ctx, session.state.Token.AccessToken, &consolev1.AppServiceCreateAppRequest{
			Name: name,
		})
		return err
	}); err != nil {
		return err
	}

	fmt.Fprintln(a.streams.Stdout, "App created.")
	fmt.Fprintf(a.streams.Stdout, "App ID: %s\n", resp.GetAppId())
	fmt.Fprintf(a.streams.Stdout, "Name: %s\n", name)
	return nil
}

func (a *app) runAppGet(ctx context.Context, global globalOptions, args []string) error {
	opts := appGetOptions{}

	fs := flag.NewFlagSet("app get", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.BoolVar(&opts.ShowValues, "show-values", false, "Show full API key values instead of masked values")
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s app get [--show-values] <app-id>\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 1 {
		return &ExitError{Code: 2, Message: "app get requires exactly one <app-id> argument"}
	}

	appID := strings.TrimSpace(fs.Arg(0))
	if appID == "" {
		return &ExitError{Code: 2, Message: "app ID is required"}
	}

	var resp *consolev1.AppServiceGetAppResponse
	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		var err error
		resp, err = session.client.GetApp(ctx, session.state.Token.AccessToken, appID)
		return err
	}); err != nil {
		return err
	}
	if resp.GetApp() == nil {
		return errors.New("get app returned no app")
	}

	a.printApp(resp.GetApp(), opts.ShowValues)
	return nil
}

func (a *app) runAppDelete(ctx context.Context, global globalOptions, args []string) error {
	opts := deleteOptions{}

	fs := flag.NewFlagSet("app delete", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.BoolVar(&opts.Yes, "yes", false, "Confirm deletion without prompting")
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s app delete [--yes] <app-id>\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 1 {
		return &ExitError{Code: 2, Message: "app delete requires exactly one <app-id> argument"}
	}
	if !opts.Yes {
		return &ExitError{Code: 2, Message: "refusing to delete app without --yes"}
	}

	appID := strings.TrimSpace(fs.Arg(0))
	if appID == "" {
		return &ExitError{Code: 2, Message: "app ID is required"}
	}

	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		return session.client.DeleteApp(ctx, session.state.Token.AccessToken, appID)
	}); err != nil {
		return err
	}

	fmt.Fprintf(a.streams.Stdout, "App deleted: %s\n", appID)
	return nil
}

func (a *app) runAPIKeyList(ctx context.Context, global globalOptions, args []string) error {
	opts := apiKeyListOptions{
		listOptions: listOptions{PageSize: defaultListPageSize},
	}

	fs := flag.NewFlagSet("api-key list", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.IntVar(&opts.PageSize, "page-size", opts.PageSize, "Number of API keys to fetch")
	fs.StringVar(&opts.PageToken, "page-token", "", "Pagination token returned by a previous list command")
	fs.BoolVar(&opts.ShowValues, "show-values", false, "Show full API key values instead of masked values")
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s api-key list [--page-size N] [--page-token TOKEN] [--show-values] <app-id>\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 1 {
		return &ExitError{Code: 2, Message: "api-key list requires exactly one <app-id> argument"}
	}
	if opts.PageSize <= 0 {
		return &ExitError{Code: 2, Message: "--page-size must be greater than zero"}
	}

	appID := strings.TrimSpace(fs.Arg(0))
	if appID == "" {
		return &ExitError{Code: 2, Message: "app ID is required"}
	}

	var resp *consolev1.AppServiceListAPIKeysResponse
	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		var err error
		resp, err = session.client.ListAPIKeys(ctx, session.state.Token.AccessToken, appID, &consolev1.AppServiceListAPIKeysRequest{
			AppId: appID,
			Pagination: &jsonapiv1.PaginationRequest{
				PageSize:  int32(opts.PageSize),
				PageToken: strings.TrimSpace(opts.PageToken),
			},
		})
		return err
	}); err != nil {
		return err
	}

	apiKeys := resp.GetApiKeys()
	if len(apiKeys) == 0 {
		fmt.Fprintf(a.streams.Stdout, "No API keys found for app %s.\n", appID)
		return nil
	}

	fmt.Fprintf(a.streams.Stdout, "App ID: %s\n", appID)
	tw := tabwriter.NewWriter(a.streams.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "API KEY\tCREATED AT")
	for _, item := range apiKeys {
		fmt.Fprintf(tw, "%s\t%s\n",
			formatAPIKeyValue(item.GetApiKey(), opts.ShowValues),
			formatProtoTimestamp(item.GetCreatedAt()),
		)
	}
	_ = tw.Flush()

	printPagination(a.streams.Stdout, resp.GetPagination())
	return nil
}

func (a *app) runAPIKeyCreate(ctx context.Context, global globalOptions, args []string) error {
	fs := flag.NewFlagSet("api-key create", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s api-key create <app-id>\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 1 {
		return &ExitError{Code: 2, Message: "api-key create requires exactly one <app-id> argument"}
	}

	appID := strings.TrimSpace(fs.Arg(0))
	if appID == "" {
		return &ExitError{Code: 2, Message: "app ID is required"}
	}

	var resp *consolev1.AppServiceCreateAPIKeyResponse
	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		var err error
		resp, err = session.client.CreateAPIKey(ctx, session.state.Token.AccessToken, appID)
		return err
	}); err != nil {
		return err
	}
	if resp.GetApiKey() == nil {
		return errors.New("create api key returned no api key")
	}

	fmt.Fprintln(a.streams.Stdout, "API key created.")
	fmt.Fprintf(a.streams.Stdout, "App ID: %s\n", appID)
	fmt.Fprintf(a.streams.Stdout, "API key: %s\n", resp.GetApiKey().GetApiKey())
	if ts := resp.GetApiKey().GetCreatedAt(); ts != nil {
		fmt.Fprintf(a.streams.Stdout, "Created at: %s\n", formatProtoTimestamp(ts))
	}
	fmt.Fprintln(a.streams.Stdout, "Store this API key securely. It may not be shown again in every client.")
	return nil
}

func (a *app) runAPIKeyDelete(ctx context.Context, global globalOptions, args []string) error {
	opts := deleteOptions{}

	fs := flag.NewFlagSet("api-key delete", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.BoolVar(&opts.Yes, "yes", false, "Confirm deletion without prompting")
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s api-key delete [--yes] <app-id> <api-key>\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 2 {
		return &ExitError{Code: 2, Message: "api-key delete requires exactly two arguments: <app-id> <api-key>"}
	}
	if !opts.Yes {
		return &ExitError{Code: 2, Message: "refusing to delete API key without --yes"}
	}

	appID := strings.TrimSpace(fs.Arg(0))
	apiKey := strings.TrimSpace(fs.Arg(1))
	if appID == "" {
		return &ExitError{Code: 2, Message: "app ID is required"}
	}
	if apiKey == "" {
		return &ExitError{Code: 2, Message: "API key is required"}
	}

	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		return session.client.DeleteAPIKey(ctx, session.state.Token.AccessToken, appID, apiKey)
	}); err != nil {
		return err
	}

	fmt.Fprintf(a.streams.Stdout, "API key deleted for app %s.\n", appID)
	return nil
}

func (a *app) withAuthenticatedSession(ctx context.Context, global globalOptions, fn func(session *authedSession) error) error {
	session, err := a.prepareAuthenticatedSession(ctx, global)
	if err != nil {
		return err
	}

	if err := fn(session); err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusUnauthorized {
			refreshed, refreshErr := a.refreshState(ctx, session.client, session.state)
			if refreshErr != nil {
				return refreshErr
			}
			session.state = refreshed
			if err := a.store.Save(session.state); err != nil {
				return fmt.Errorf("save auth state: %w", err)
			}
			return fn(session)
		}
		return err
	}

	return nil
}

func (a *app) prepareAuthenticatedSession(ctx context.Context, global globalOptions) (*authedSession, error) {
	state, err := a.store.Load()
	if err != nil {
		if errors.Is(err, ErrNotLoggedIn) {
			return nil, &ExitError{Code: 1, Message: "not logged in"}
		}
		return nil, err
	}

	resolved, err := resolveOptions(global, state)
	if err != nil {
		return nil, err
	}
	state.BaseURL = resolved.BaseURL

	client := NewAPIClient(resolved.BaseURL)
	refreshed, err := a.ensureFreshToken(ctx, client, state)
	if err != nil {
		return nil, err
	}
	state = refreshed
	if err := a.store.Save(state); err != nil {
		return nil, fmt.Errorf("save auth state: %w", err)
	}

	return &authedSession{
		client: client,
		state:  state,
	}, nil
}

func (a *app) printApp(item *consolev1.App, showValues bool) {
	fmt.Fprintf(a.streams.Stdout, "App ID: %s\n", item.GetAppId())
	fmt.Fprintf(a.streams.Stdout, "Name: %s\n", defaultIfEmpty(item.GetName(), "-"))
	fmt.Fprintf(a.streams.Stdout, "API keys: %d\n", len(item.GetApiKeys()))
	if ts := item.GetCreatedAt(); ts != nil {
		fmt.Fprintf(a.streams.Stdout, "Created at: %s\n", formatProtoTimestamp(ts))
	}
	if ts := item.GetUpdatedAt(); ts != nil {
		fmt.Fprintf(a.streams.Stdout, "Updated at: %s\n", formatProtoTimestamp(ts))
	}

	if len(item.GetApiKeys()) == 0 {
		return
	}

	fmt.Fprintln(a.streams.Stdout)
	tw := tabwriter.NewWriter(a.streams.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "API KEY\tCREATED AT")
	for _, apiKey := range item.GetApiKeys() {
		fmt.Fprintf(tw, "%s\t%s\n",
			formatAPIKeyValue(apiKey.GetApiKey(), showValues),
			formatProtoTimestamp(apiKey.GetCreatedAt()),
		)
	}
	_ = tw.Flush()
}

func printPagination(writer io.Writer, pagination *jsonapiv1.PaginationResponse) {
	if pagination == nil {
		return
	}
	if pagination.GetNextPageToken() != "" {
		fmt.Fprintf(writer, "Next page token: %s\n", pagination.GetNextPageToken())
	}
	if pagination.GetTotalCount() > 0 {
		fmt.Fprintf(writer, "Total count: %d\n", pagination.GetTotalCount())
	}
}

func formatProtoTimestamp(ts interface{ AsTime() time.Time }) string {
	if ts == nil {
		return "-"
	}

	value := ts.AsTime().UTC()
	if value.IsZero() {
		return "-"
	}
	return value.Format(time.RFC3339)
}

func formatAPIKeyValue(value string, showValues bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	if showValues {
		return value
	}
	if len(value) <= 14 {
		return value
	}
	return value[:8] + strings.Repeat("*", len(value)-14) + value[len(value)-6:]
}

func defaultIfEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

package avtkitcli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	consolev1 "github.com/spatialwalk/open-platform-cli/api/generated/console/v1"
)

const (
	defaultSessionTokenTTL = time.Hour
	maxSessionTokenTTL     = 24 * time.Hour
)

type sessionTokenCreateOptions struct {
	APIKey       string
	ExpireIn     time.Duration
	ModelVersion string
}

func (a *app) runSessionToken(ctx context.Context, global globalOptions, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s session-token <create>\n", cliName)
		return &ExitError{Code: 2}
	}

	switch args[0] {
	case "create":
		return a.runSessionTokenCreate(ctx, global, args[1:])
	case "help", "-h", "--help":
		fmt.Fprintf(a.streams.Stderr, "Usage: %s session-token <create>\n", cliName)
		return nil
	default:
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown session-token command %q", args[0])}
	}
}

func (a *app) runSessionTokenCreate(ctx context.Context, global globalOptions, args []string) error {
	opts := sessionTokenCreateOptions{
		ExpireIn: defaultSessionTokenTTL,
	}

	fs := flag.NewFlagSet("session-token create", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.StringVar(&opts.APIKey, "api-key", "", "Use a specific API key from the app instead of the first available key")
	fs.DurationVar(&opts.ExpireIn, "expire-in", opts.ExpireIn, "How long the session token should remain valid (max 24h)")
	fs.StringVar(&opts.ModelVersion, "model-version", "", "Optional model/service version embedded in the session token")
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s session-token create [--api-key KEY] [--expire-in DURATION] [--model-version VERSION] <app-id>\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 1 {
		return &ExitError{Code: 2, Message: "session-token create requires exactly one <app-id> argument"}
	}
	if opts.ExpireIn <= 0 {
		return &ExitError{Code: 2, Message: "--expire-in must be greater than zero"}
	}
	if opts.ExpireIn > maxSessionTokenTTL {
		return &ExitError{Code: 2, Message: "--expire-in cannot be greater than 24h"}
	}

	appID := strings.TrimSpace(fs.Arg(0))
	if appID == "" {
		return &ExitError{Code: 2, Message: "app ID is required"}
	}

	var (
		resp            *consolev1.CreateSessionTokenResponse
		selectedAPIKey  string
		selectionNotice string
		expiresAt       time.Time
		modelVersion    string
	)
	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		appResp, err := session.client.GetApp(ctx, session.state.Token.AccessToken, appID)
		if err != nil {
			return err
		}
		if appResp.GetApp() == nil {
			return errors.New("get app returned no app")
		}

		selectedAPIKey, selectionNotice, err = selectAppAPIKey(appID, appResp.GetApp().GetApiKeys(), opts.APIKey)
		if err != nil {
			return err
		}

		expiresAt = time.Now().UTC().Add(opts.ExpireIn)
		modelVersion = strings.TrimSpace(opts.ModelVersion)
		resp, err = session.client.CreateSessionToken(ctx, selectedAPIKey, &consolev1.CreateSessionTokenRequest{
			ExpireAt:     expiresAt.Unix(),
			ModelVersion: modelVersion,
		})
		return err
	}); err != nil {
		return err
	}
	if resp.GetSessionToken() == "" {
		return errors.New("create session token returned no session token")
	}

	if selectionNotice != "" {
		fmt.Fprintf(a.streams.Stderr, "warning: %s\n", selectionNotice)
	}

	fmt.Fprintln(a.streams.Stdout, "Session token created.")
	fmt.Fprintf(a.streams.Stdout, "App ID: %s\n", appID)
	fmt.Fprintf(a.streams.Stdout, "API key: %s\n", formatAPIKeyValue(selectedAPIKey, false))
	fmt.Fprintf(a.streams.Stdout, "Expires at: %s\n", expiresAt.Format(time.RFC3339))
	if modelVersion != "" {
		fmt.Fprintf(a.streams.Stdout, "Model version: %s\n", modelVersion)
	}
	fmt.Fprintf(a.streams.Stdout, "Session token: %s\n", resp.GetSessionToken())
	return nil
}

func selectAppAPIKey(appID string, apiKeys []*consolev1.APIKey, requested string) (string, string, error) {
	requested = strings.TrimSpace(requested)

	available := make([]string, 0, len(apiKeys))
	for _, item := range apiKeys {
		if value := strings.TrimSpace(item.GetApiKey()); value != "" {
			available = append(available, value)
		}
	}

	if len(available) == 0 {
		return "", "", fmt.Errorf("app %s has no API keys; run `%s api-key create %s` first", appID, cliName, appID)
	}

	if requested != "" {
		for _, value := range available {
			if value == requested {
				return value, "", nil
			}
		}
		return "", "", fmt.Errorf("API key not found for app %s", appID)
	}

	notice := ""
	if len(available) > 1 {
		notice = "multiple API keys found; using the first available key (pass --api-key to choose a different key)"
	}
	return available[0], notice, nil
}

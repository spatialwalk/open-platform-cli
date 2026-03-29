package avtkitcli

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	consolev2 "github.com/spatialwalk/open-platform-cli/api/generated/console/v2"
	jsonapiv1 "github.com/spatialwalk/open-platform-cli/api/generated/jsonapi/v1"
)

type publicAvatarListOptions struct {
	listOptions
	ShowCoverURLs bool
}

func (a *app) runPublicAvatar(ctx context.Context, global globalOptions, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s avatar <list|ls>\n", cliName)
		return &ExitError{Code: 2}
	}

	switch args[0] {
	case "list", "ls":
		return a.runPublicAvatarList(ctx, global, args[1:])
	case "help", "-h", "--help":
		fmt.Fprintf(a.streams.Stderr, "Usage: %s avatar <list|ls>\n", cliName)
		return nil
	default:
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown avatar command %q", args[0])}
	}
}

func (a *app) runPublicAvatarList(ctx context.Context, global globalOptions, args []string) error {
	opts := publicAvatarListOptions{
		listOptions: listOptions{PageSize: defaultListPageSize},
	}

	fs := flag.NewFlagSet("avatar list", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.IntVar(&opts.PageSize, "page-size", opts.PageSize, "Number of public avatars to fetch")
	fs.StringVar(&opts.PageToken, "page-token", "", "Pagination token returned by a previous list command")
	fs.BoolVar(&opts.ShowCoverURLs, "show-cover-urls", false, "Include cover URLs in table output")
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s avatar <list|ls> [--page-size N] [--page-token TOKEN] [--show-cover-urls]\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 0 {
		return &ExitError{Code: 2, Message: "avatar list does not accept positional arguments"}
	}
	if opts.PageSize <= 0 {
		return &ExitError{Code: 2, Message: "--page-size must be greater than zero"}
	}

	var resp *consolev2.ListPublicAvatarsResponse
	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		var err error
		resp, err = session.client.ListPublicAvatars(ctx, session.state.Token.AccessToken, &consolev2.ListPublicAvatarsRequest{
			Pagination: &jsonapiv1.PaginationRequest{
				PageSize:  int32(opts.PageSize),
				PageToken: strings.TrimSpace(opts.PageToken),
			},
		})
		return err
	}); err != nil {
		return err
	}

	publicAvatars := resp.GetPublicAvatars()
	if len(publicAvatars) == 0 {
		fmt.Fprintln(a.streams.Stdout, "No public avatars found.")
		return nil
	}

	tw := tabwriter.NewWriter(a.streams.Stdout, 0, 0, 2, ' ', 0)
	if opts.ShowCoverURLs {
		fmt.Fprintln(tw, "AVATAR ID\tNAME\tCOVER URL")
		for _, item := range publicAvatars {
			fmt.Fprintf(tw, "%s\t%s\t%s\n",
				defaultIfEmpty(item.GetId(), "-"),
				defaultIfEmpty(item.GetName(), "-"),
				defaultIfEmpty(item.GetCoverUrl(), "-"),
			)
		}
	} else {
		fmt.Fprintln(tw, "AVATAR ID\tNAME")
		for _, item := range publicAvatars {
			fmt.Fprintf(tw, "%s\t%s\n",
				defaultIfEmpty(item.GetId(), "-"),
				defaultIfEmpty(item.GetName(), "-"),
			)
		}
	}
	_ = tw.Flush()

	printPagination(a.streams.Stdout, resp.GetPagination())
	return nil
}

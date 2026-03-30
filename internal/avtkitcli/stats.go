package avtkitcli

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	consolev2 "github.com/spatialwalk/open-platform-cli/api/generated/console/v2"
)

const (
	defaultStatsTimeRangeKey = "30d"
)

type statsUsageOptions struct {
	TimeRange string
}

type statsTimeRangePreset struct {
	Key   string
	Label string
	Enum  consolev2.UserStatsTimeRange
}

type usageTrendRow struct {
	Label      string
	DurationMs int64
}

type sessionTrendRow struct {
	Label string
	Count int64
}

var statsTimeRangePresets = []statsTimeRangePreset{
	{Key: "today", Label: "Today", Enum: consolev2.UserStatsTimeRange_USER_STATS_TIME_RANGE_TODAY},
	{Key: "7d", Label: "7 Days", Enum: consolev2.UserStatsTimeRange_USER_STATS_TIME_RANGE_7D},
	{Key: "14d", Label: "14 Days", Enum: consolev2.UserStatsTimeRange_USER_STATS_TIME_RANGE_14D},
	{Key: "30d", Label: "30 Days", Enum: consolev2.UserStatsTimeRange_USER_STATS_TIME_RANGE_30D},
	{Key: "90d", Label: "90 Days", Enum: consolev2.UserStatsTimeRange_USER_STATS_TIME_RANGE_90D},
	{Key: "1y", Label: "1 Year", Enum: consolev2.UserStatsTimeRange_USER_STATS_TIME_RANGE_1Y},
}

func (a *app) runStats(ctx context.Context, global globalOptions, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s stats <usage>\n", cliName)
		return &ExitError{Code: 2}
	}

	switch args[0] {
	case "usage":
		return a.runStatsUsage(ctx, global, args[1:])
	case "help", "-h", "--help":
		fmt.Fprintf(a.streams.Stderr, "Usage: %s stats <usage>\n", cliName)
		return nil
	default:
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown stats command %q", args[0])}
	}
}

func (a *app) runStatsUsage(ctx context.Context, global globalOptions, args []string) error {
	opts := statsUsageOptions{
		TimeRange: defaultStatsTimeRangeKey,
	}

	fs := flag.NewFlagSet("stats usage", flag.ContinueOnError)
	fs.SetOutput(a.streams.Stderr)
	fs.StringVar(&opts.TimeRange, "time-range", opts.TimeRange, "Time range preset: today, 7d, 14d, 30d, 90d, 1y")
	fs.Usage = func() {
		fmt.Fprintf(a.streams.Stderr, "Usage: %s stats usage [--time-range RANGE]\n", cliName)
	}
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}
	if fs.NArg() != 0 {
		return &ExitError{Code: 2, Message: "stats usage does not accept positional arguments"}
	}

	preset, err := parseStatsTimeRangePreset(opts.TimeRange)
	if err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}

	var (
		usageResp    *consolev2.GetUsageResponse
		sessionResp  *consolev2.GetSessionCountResponse
		realtimeResp *consolev2.GetRealtimeConcurrentConnectionsResponse
		realtimeErr  error
	)
	if err := a.withAuthenticatedSession(ctx, global, func(session *authedSession) error {
		var err error
		usageResp, err = session.client.GetUsage(ctx, session.state.Token.AccessToken, &consolev2.GetUsageRequest{
			TimeRange: preset.Enum,
		})
		if err != nil {
			return err
		}

		sessionResp, err = session.client.GetSessionCount(ctx, session.state.Token.AccessToken, &consolev2.GetSessionCountRequest{
			TimeRange: preset.Enum,
		})
		if err != nil {
			return err
		}

		realtimeResp, realtimeErr = session.client.GetRealtimeConcurrentConnections(ctx, session.state.Token.AccessToken)
		return nil
	}); err != nil {
		return err
	}

	if realtimeErr != nil {
		fmt.Fprintf(a.streams.Stderr, "warning: failed to fetch live connections: %v\n", realtimeErr)
	}

	a.printStatsUsage(preset, usageResp, sessionResp, realtimeResp)
	return nil
}

func parseStatsTimeRangePreset(value string) (statsTimeRangePreset, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, preset := range statsTimeRangePresets {
		if preset.Key == normalized {
			return preset, nil
		}
	}

	return statsTimeRangePreset{}, fmt.Errorf("invalid --time-range %q (expected one of: today, 7d, 14d, 30d, 90d, 1y)", value)
}

func (a *app) printStatsUsage(preset statsTimeRangePreset, usageResp *consolev2.GetUsageResponse, sessionResp *consolev2.GetSessionCountResponse, realtimeResp *consolev2.GetRealtimeConcurrentConnectionsResponse) {
	fmt.Fprintln(a.streams.Stdout, "Usage Statistics")
	fmt.Fprintf(a.streams.Stdout, "View your API usage\n\n")
	fmt.Fprintf(a.streams.Stdout, "Time Range: %s (%s)\n", preset.Key, preset.Label)
	if realtimeResp != nil {
		fmt.Fprintf(a.streams.Stdout, "Live Connections: %d\n", realtimeResp.GetConcurrentConnections())
	} else {
		fmt.Fprintln(a.streams.Stdout, "Live Connections: unavailable")
	}
	fmt.Fprintf(a.streams.Stdout, "Total Duration: %s\n", formatDurationMilliseconds(usageResp.GetTotalConnectionDurationMs()))
	fmt.Fprintf(a.streams.Stdout, "Total Connections: %d\n", sessionResp.GetTotalSessionCount())
	fmt.Fprintf(a.streams.Stdout, "Peak Concurrent Connections: %d\n", sessionResp.GetMaxConcurrentConnections())

	fmt.Fprintln(a.streams.Stdout)
	a.printUsageTrend(buildUsageTrendRows(preset, usageResp.GetDataPoints()))
	fmt.Fprintln(a.streams.Stdout)
	a.printSessionTrend(buildSessionTrendRows(preset, sessionResp.GetDataPoints()))
}

func (a *app) printUsageTrend(rows []usageTrendRow) {
	fmt.Fprintln(a.streams.Stdout, "Connection Duration Trend")
	if len(rows) == 0 {
		fmt.Fprintln(a.streams.Stdout, "No data available.")
		fmt.Fprintln(a.streams.Stdout, "Start a session to see your usage data here.")
		return
	}

	tw := tabwriter.NewWriter(a.streams.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PERIOD\tDURATION")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\n", row.Label, formatDurationMilliseconds(row.DurationMs))
	}
	_ = tw.Flush()
}

func (a *app) printSessionTrend(rows []sessionTrendRow) {
	fmt.Fprintln(a.streams.Stdout, "Connection Count Trend")
	if len(rows) == 0 {
		fmt.Fprintln(a.streams.Stdout, "No data available.")
		fmt.Fprintln(a.streams.Stdout, "Start a session to see your usage data here.")
		return
	}

	tw := tabwriter.NewWriter(a.streams.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PERIOD\tCONNECTIONS")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%d\n", row.Label, row.Count)
	}
	_ = tw.Flush()
}

func buildUsageTrendRows(preset statsTimeRangePreset, dataPoints []*consolev2.UsageDataPoint) []usageTrendRow {
	if len(dataPoints) == 0 {
		return nil
	}

	switch preset.Key {
	case "today":
		return fillTodayUsageRows(dataPoints)
	case "1y":
		return groupMonthlyUsageRows(dataPoints)
	case "30d":
		return groupNDaysUsageRows(dataPoints, 30, 3)
	case "90d":
		return groupNDaysUsageRows(dataPoints, 90, 7)
	default:
		return fillDailyUsageRows(dataPoints, statsTimeRangeDays(preset.Key))
	}
}

func buildSessionTrendRows(preset statsTimeRangePreset, dataPoints []*consolev2.SessionCountDataPoint) []sessionTrendRow {
	if len(dataPoints) == 0 {
		return nil
	}

	switch preset.Key {
	case "today":
		return fillTodaySessionRows(dataPoints)
	case "1y":
		return groupMonthlySessionRows(dataPoints)
	case "30d":
		return groupNDaysSessionRows(dataPoints, 30, 3)
	case "90d":
		return groupNDaysSessionRows(dataPoints, 90, 7)
	default:
		return fillDailySessionRows(dataPoints, statsTimeRangeDays(preset.Key))
	}
}

func statsTimeRangeDays(key string) int {
	switch key {
	case "today":
		return 1
	case "7d":
		return 7
	case "14d":
		return 14
	case "30d":
		return 30
	case "90d":
		return 90
	case "1y":
		return 365
	default:
		return 0
	}
}

func fillTodayUsageRows(dataPoints []*consolev2.UsageDataPoint) []usageTrendRow {
	hourMap := make(map[int]int64, len(dataPoints))
	for _, item := range dataPoints {
		ts := protoTimestampAsLocal(item.GetTimestamp())
		if ts.IsZero() {
			continue
		}
		hourMap[ts.Hour()] = item.GetConnectionDurationMs()
	}

	rows := make([]usageTrendRow, 0, 24)
	for hour := 0; hour < 24; hour++ {
		rows = append(rows, usageTrendRow{
			Label:      fmt.Sprintf("%02d:00", hour),
			DurationMs: hourMap[hour],
		})
	}
	return rows
}

func fillTodaySessionRows(dataPoints []*consolev2.SessionCountDataPoint) []sessionTrendRow {
	hourMap := make(map[int]int64, len(dataPoints))
	for _, item := range dataPoints {
		ts := protoTimestampAsLocal(item.GetTimestamp())
		if ts.IsZero() {
			continue
		}
		hourMap[ts.Hour()] = int64(item.GetSessionCount())
	}

	rows := make([]sessionTrendRow, 0, 24)
	for hour := 0; hour < 24; hour++ {
		rows = append(rows, sessionTrendRow{
			Label: fmt.Sprintf("%02d:00", hour),
			Count: hourMap[hour],
		})
	}
	return rows
}

func fillDailyUsageRows(dataPoints []*consolev2.UsageDataPoint, days int) []usageTrendRow {
	dateMap := make(map[string]int64, len(dataPoints))
	for _, item := range dataPoints {
		ts := protoTimestampAsLocal(item.GetTimestamp())
		if ts.IsZero() {
			continue
		}
		dateMap[localDateKey(ts)] = item.GetConnectionDurationMs()
	}

	rows := make([]usageTrendRow, 0, days)
	now := time.Now().In(time.Local)
	for offset := days - 1; offset >= 0; offset-- {
		day := time.Date(now.Year(), now.Month(), now.Day()-offset, 0, 0, 0, 0, time.Local)
		rows = append(rows, usageTrendRow{
			Label:      formatDateLabel(day),
			DurationMs: dateMap[localDateKey(day)],
		})
	}
	return rows
}

func fillDailySessionRows(dataPoints []*consolev2.SessionCountDataPoint, days int) []sessionTrendRow {
	dateMap := make(map[string]int64, len(dataPoints))
	for _, item := range dataPoints {
		ts := protoTimestampAsLocal(item.GetTimestamp())
		if ts.IsZero() {
			continue
		}
		dateMap[localDateKey(ts)] = int64(item.GetSessionCount())
	}

	rows := make([]sessionTrendRow, 0, days)
	now := time.Now().In(time.Local)
	for offset := days - 1; offset >= 0; offset-- {
		day := time.Date(now.Year(), now.Month(), now.Day()-offset, 0, 0, 0, 0, time.Local)
		rows = append(rows, sessionTrendRow{
			Label: formatDateLabel(day),
			Count: dateMap[localDateKey(day)],
		})
	}
	return rows
}

func groupMonthlyUsageRows(dataPoints []*consolev2.UsageDataPoint) []usageTrendRow {
	monthMap := make(map[string]int64, len(dataPoints))
	for _, item := range dataPoints {
		ts := protoTimestampAsLocal(item.GetTimestamp())
		if ts.IsZero() {
			continue
		}
		key := localMonthKey(ts)
		monthMap[key] += item.GetConnectionDurationMs()
	}

	rows := make([]usageTrendRow, 0, 12)
	now := time.Now().In(time.Local)
	for offset := 11; offset >= 0; offset-- {
		monthStart := time.Date(now.Year(), now.Month()-time.Month(offset), 1, 0, 0, 0, 0, time.Local)
		rows = append(rows, usageTrendRow{
			Label:      formatMonthLabel(monthStart),
			DurationMs: monthMap[localMonthKey(monthStart)],
		})
	}
	return rows
}

func groupMonthlySessionRows(dataPoints []*consolev2.SessionCountDataPoint) []sessionTrendRow {
	monthMap := make(map[string]int64, len(dataPoints))
	for _, item := range dataPoints {
		ts := protoTimestampAsLocal(item.GetTimestamp())
		if ts.IsZero() {
			continue
		}
		key := localMonthKey(ts)
		monthMap[key] += int64(item.GetSessionCount())
	}

	rows := make([]sessionTrendRow, 0, 12)
	now := time.Now().In(time.Local)
	for offset := 11; offset >= 0; offset-- {
		monthStart := time.Date(now.Year(), now.Month()-time.Month(offset), 1, 0, 0, 0, 0, time.Local)
		rows = append(rows, sessionTrendRow{
			Label: formatMonthLabel(monthStart),
			Count: monthMap[localMonthKey(monthStart)],
		})
	}
	return rows
}

func groupNDaysUsageRows(dataPoints []*consolev2.UsageDataPoint, totalDays, groupSize int) []usageTrendRow {
	dateMap := make(map[string]int64, len(dataPoints))
	for _, item := range dataPoints {
		ts := protoTimestampAsLocal(item.GetTimestamp())
		if ts.IsZero() {
			continue
		}
		dateMap[localDateKey(ts)] += item.GetConnectionDurationMs()
	}

	rows := make([]usageTrendRow, 0, totalDays/groupSize+1)
	now := time.Now().In(time.Local)
	for offset := totalDays - 1; offset >= 0; offset -= groupSize {
		endDate := time.Date(now.Year(), now.Month(), now.Day()-offset, 0, 0, 0, 0, time.Local)
		startOffset := minInt(offset+groupSize-1, totalDays-1)
		startDate := time.Date(now.Year(), now.Month(), now.Day()-startOffset, 0, 0, 0, 0, time.Local)

		var sum int64
		for step := 0; step < groupSize && offset-step >= 0; step++ {
			day := time.Date(now.Year(), now.Month(), now.Day()-offset+step, 0, 0, 0, 0, time.Local)
			sum += dateMap[localDateKey(day)]
		}

		rows = append(rows, usageTrendRow{
			Label:      fmt.Sprintf("%s-%s", formatDateLabel(startDate), formatDateLabel(endDate)),
			DurationMs: sum,
		})
	}
	return rows
}

func groupNDaysSessionRows(dataPoints []*consolev2.SessionCountDataPoint, totalDays, groupSize int) []sessionTrendRow {
	dateMap := make(map[string]int64, len(dataPoints))
	for _, item := range dataPoints {
		ts := protoTimestampAsLocal(item.GetTimestamp())
		if ts.IsZero() {
			continue
		}
		dateMap[localDateKey(ts)] += int64(item.GetSessionCount())
	}

	rows := make([]sessionTrendRow, 0, totalDays/groupSize+1)
	now := time.Now().In(time.Local)
	for offset := totalDays - 1; offset >= 0; offset -= groupSize {
		endDate := time.Date(now.Year(), now.Month(), now.Day()-offset, 0, 0, 0, 0, time.Local)
		startOffset := minInt(offset+groupSize-1, totalDays-1)
		startDate := time.Date(now.Year(), now.Month(), now.Day()-startOffset, 0, 0, 0, 0, time.Local)

		var sum int64
		for step := 0; step < groupSize && offset-step >= 0; step++ {
			day := time.Date(now.Year(), now.Month(), now.Day()-offset+step, 0, 0, 0, 0, time.Local)
			sum += dateMap[localDateKey(day)]
		}

		rows = append(rows, sessionTrendRow{
			Label: fmt.Sprintf("%s-%s", formatDateLabel(startDate), formatDateLabel(endDate)),
			Count: sum,
		})
	}
	return rows
}

func formatDurationMilliseconds(ms int64) string {
	if ms <= 0 {
		return "0"
	}

	totalSeconds := ms / int64(time.Second/time.Millisecond)
	hours := totalSeconds / int64(time.Hour/time.Second)
	minutes := (totalSeconds % int64(time.Hour/time.Second)) / int64(time.Minute/time.Second)
	seconds := totalSeconds % int64(time.Minute/time.Second)

	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func protoTimestampAsLocal(ts interface{ AsTime() time.Time }) time.Time {
	if ts == nil {
		return time.Time{}
	}
	value := ts.AsTime()
	if value.IsZero() {
		return time.Time{}
	}
	return value.In(time.Local)
}

func localDateKey(value time.Time) string {
	return fmt.Sprintf("%04d-%02d-%02d", value.Year(), value.Month(), value.Day())
}

func localMonthKey(value time.Time) string {
	return fmt.Sprintf("%04d-%02d", value.Year(), value.Month())
}

func formatDateLabel(value time.Time) string {
	return fmt.Sprintf("%02d/%02d", value.Month(), value.Day())
}

func formatMonthLabel(value time.Time) string {
	return fmt.Sprintf("%04d/%02d", value.Year(), value.Month())
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

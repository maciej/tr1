package bbc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"tr1/internal/programmes"
)

const DefaultScheduleURL = "https://rms.api.bbc.co.uk/v2/broadcasts/schedules/bbc_world_service"

var datedScheduleURLRE = regexp.MustCompile(`/\d{4}-\d{2}-\d{2}$`)

func Fetch(ctx context.Context, client *http.Client, sourceURL string) (programmes.Schedule, error) {
	if client == nil {
		client = http.DefaultClient
	}
	scheduleURL, scheduleDate := scheduleURLForDate(sourceURL, time.Now().UTC())
	data, err := fetchBytes(ctx, client, scheduleURL)
	if err != nil {
		return programmes.Schedule{}, err
	}
	entries, err := ParseSchedule(data, scheduleDate)
	if err != nil {
		return programmes.Schedule{}, err
	}
	return programmes.Schedule{
		Station:   "BBC World Service",
		SourceURL: scheduleURL,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Entries:   entries,
	}, nil
}

func fetchBytes(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tr1-lab/0 (+https://github.com/)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s returned %s", rawURL, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func scheduleURLForDate(raw string, now time.Time) (string, time.Time) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultScheduleURL
	}
	date := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	dateText := date.Format("2006-01-02")
	if strings.Contains(raw, "{date}") {
		return strings.ReplaceAll(raw, "{date}", dateText), date
	}
	if datedScheduleURLRE.MatchString(raw) {
		if parsed, err := time.Parse("2006-01-02", raw[len(raw)-len(dateText):]); err == nil {
			date = parsed
		}
		return raw, date
	}
	parsed, err := url.Parse(raw)
	if err == nil {
		queryDate := strings.TrimSpace(parsed.Query().Get("date"))
		if parsedDate, parseErr := time.Parse("2006-01-02", queryDate); parseErr == nil {
			return raw, parsedDate
		}
	}
	return strings.TrimRight(raw, "/") + "/" + dateText, date
}

type broadcastsResponse struct {
	Data []broadcast `json:"data"`
}

type broadcast struct {
	ID           string            `json:"id"`
	URN          string            `json:"urn"`
	Start        string            `json:"start"`
	End          string            `json:"end"`
	Duration     int               `json:"duration"`
	Titles       broadcastTitles   `json:"titles"`
	ImageURL     string            `json:"image_url"`
	Container    broadcastItemRef  `json:"container"`
	PlayableItem broadcastPlayable `json:"playable_item"`
}

type broadcastTitles struct {
	Primary   string `json:"primary"`
	Secondary string `json:"secondary"`
	Tertiary  string `json:"tertiary"`
}

type broadcastItemRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type broadcastPlayable struct {
	ID        string           `json:"id"`
	URN       string           `json:"urn"`
	Titles    broadcastTitles  `json:"titles"`
	Container broadcastItemRef `json:"container"`
}

func ParseSchedule(data []byte, scheduleDate time.Time) ([]programmes.Entry, error) {
	var response broadcastsResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	dayStart := time.Date(scheduleDate.UTC().Year(), scheduleDate.UTC().Month(), scheduleDate.UTC().Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.AddDate(0, 0, 1)
	var entries []programmes.Entry
	for _, item := range response.Data {
		entry, start, end, ok := parseBroadcast(item)
		if !ok || !end.After(dayStart) || !start.Before(dayEnd) {
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("BBC schedule parser found no programme entries")
	}
	return entries, nil
}

func parseBroadcast(item broadcast) (programmes.Entry, time.Time, time.Time, bool) {
	start, err := time.Parse(time.RFC3339, item.Start)
	if err != nil {
		return programmes.Entry{}, time.Time{}, time.Time{}, false
	}
	end, err := time.Parse(time.RFC3339, item.End)
	if err != nil {
		if item.Duration > 0 {
			end = start.Add(time.Duration(item.Duration) * time.Second)
		} else {
			return programmes.Entry{}, time.Time{}, time.Time{}, false
		}
	}
	programme := firstNonEmpty(item.Titles.Primary, item.Container.Title, item.PlayableItem.Container.Title, item.PlayableItem.Titles.Primary)
	if programme == "" {
		return programmes.Entry{}, time.Time{}, time.Time{}, false
	}
	episode := firstNonEmpty(item.Titles.Secondary, item.Titles.Tertiary, item.PlayableItem.Titles.Secondary)
	if episode == programme {
		episode = ""
	}
	weekday := start.UTC().Weekday()
	entry := programmes.Entry{
		DayIndex:     weekdayIndex(weekday),
		Day:          weekday.String(),
		Time:         start.UTC().Format("15:04"),
		Programme:    programme,
		ProgrammeURL: programmeURL(firstNonEmpty(item.Container.ID, item.PlayableItem.Container.ID)),
		Episode:      episode,
		EpisodeURL:   programmeURL(firstNonEmpty(episodeIDFromURN(item.URN), episodeIDFromURN(item.PlayableItem.URN), item.PlayableItem.ID)),
		ImageURL:     usableImageURL(item.ImageURL),
		PodcastID:    item.ID,
		Duration:     durationLabel(end.Sub(start)),
	}
	return entry, start.UTC(), end.UTC(), true
}

func weekdayIndex(day time.Weekday) int {
	if day == time.Sunday {
		return 6
	}
	return int(day) - 1
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
		if value != "" {
			return value
		}
	}
	return ""
}

func programmeURL(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return "https://www.bbc.co.uk/programmes/" + id
}

func episodeIDFromURN(urn string) string {
	parts := strings.Split(strings.TrimSpace(urn), ":")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func usableImageURL(raw string) string {
	return strings.ReplaceAll(strings.TrimSpace(raw), "{recipe}", "320x180")
}

func durationLabel(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	totalSeconds := int(duration.Round(time.Second).Seconds())
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	if seconds == 0 {
		if minutes == 1 {
			return "1 min"
		}
		return fmt.Sprintf("%d min", minutes)
	}
	return fmt.Sprintf("%d min %d sec", minutes, seconds)
}

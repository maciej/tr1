package tokfm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"

	"tr1/internal/programmes"
)

const DefaultScheduleURL = "https://audycje.tokfm.pl/ramowka"

func Fetch(ctx context.Context, client *http.Client, sourceURL string) (programmes.Schedule, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if strings.TrimSpace(sourceURL) == "" {
		sourceURL = DefaultScheduleURL
	}
	pageHTML, err := fetchText(ctx, client, sourceURL)
	if err != nil {
		return programmes.Schedule{}, err
	}
	islandURL, err := extractScheduleIslandURL(sourceURL, pageHTML)
	if err != nil {
		return programmes.Schedule{}, err
	}
	fragment, err := fetchText(ctx, client, islandURL)
	if err != nil {
		return programmes.Schedule{}, err
	}
	entries, err := ParseSchedule(fragment, islandURL)
	if err != nil {
		return programmes.Schedule{}, err
	}
	return programmes.Schedule{
		Station:   "TokFM",
		SourceURL: sourceURL,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Entries:   entries,
	}, nil
}

func fetchText(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tr1-lab/0 (+https://github.com/)")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GET %s returned %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ExtractScheduleIslandURL(pageURL, pageHTML string) (string, error) {
	return extractScheduleIslandURL(pageURL, pageHTML)
}

func extractScheduleIslandURL(pageURL, pageHTML string) (string, error) {
	doc, err := html.Parse(strings.NewReader(pageHTML))
	if err != nil {
		return "", err
	}
	var found string
	walkHTML(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "link" {
			return true
		}
		href := htmlAttr(n, "href")
		if strings.Contains(href, "/_server-islands/ScheduleListIsland") || strings.Contains(href, "ScheduleListIsland") {
			found = html.UnescapeString(href)
			return false
		}
		return true
	})
	if found == "" {
		return "", fmt.Errorf("could not find TOK FM ScheduleListIsland endpoint in %s", pageURL)
	}
	return resolveReferenceURL(pageURL, found)
}

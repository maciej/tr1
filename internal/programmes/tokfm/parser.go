package tokfm

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"tr1/internal/programmes"
)

var dayNames = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
var timeRE = regexp.MustCompile(`^\d{2}:\d{2}$`)

func ParseSchedule(fragment, baseURL string) ([]programmes.Entry, error) {
	doc, err := html.Parse(strings.NewReader("<html><body>" + fragment + "</body></html>"))
	if err != nil {
		return nil, err
	}
	var entries []programmes.Entry
	walkHTML(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "ul" || !htmlClassContains(n, "day") {
			return true
		}
		dayIndex, err := strconv.Atoi(strings.TrimSpace(htmlAttr(n, "data-day")))
		if err != nil || dayIndex < 0 || dayIndex >= len(dayNames) {
			return true
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode || child.Data != "li" {
				continue
			}
			entry := parseEntry(child, baseURL)
			if entry.Time == "" || entry.Programme == "" {
				continue
			}
			entry.DayIndex = dayIndex
			entry.Day = dayNames[dayIndex]
			entries = append(entries, entry)
		}
		return true
	})
	if len(entries) == 0 {
		return nil, fmt.Errorf("TOK FM schedule parser found no programme entries")
	}
	return entries, nil
}

func parseEntry(n *html.Node, baseURL string) programmes.Entry {
	entry := programmes.Entry{
		Time: strings.TrimSpace(firstTextMatching(n, timeRE)),
	}
	if img := firstElement(n, func(node *html.Node) bool { return node.Data == "img" }); img != nil {
		entry.ImageURL, _ = resolveReferenceURL(baseURL, htmlAttr(img, "src"))
	}
	headings := elementsMatching(n, func(node *html.Node) bool {
		return node.Data == "h3" && htmlClassContains(node, "tok-schedule__program--name")
	})
	if len(headings) > 0 {
		entry.Programme = cleanText(textContent(headings[0]))
		if link := firstElement(headings[0], func(node *html.Node) bool { return node.Data == "a" }); link != nil {
			entry.ProgrammeURL, _ = resolveReferenceURL(baseURL, htmlAttr(link, "href"))
		}
	}
	if len(headings) > 1 {
		entry.Episode = cleanText(textContent(headings[1]))
		if link := firstElement(headings[1], func(node *html.Node) bool { return node.Data == "a" }); link != nil {
			entry.EpisodeURL, _ = resolveReferenceURL(baseURL, htmlAttr(link, "href"))
		}
	}
	entry.Hosts = presenterNames(n)
	if button := firstElement(n, func(node *html.Node) bool {
		return node.Data == "button" && htmlClassContains(node, "tok-podcasts__button--play")
	}); button != nil {
		entry.PodcastID = strings.TrimSpace(htmlAttr(button, "data-id"))
		entry.Duration = cleanText(textContent(firstElement(button, func(node *html.Node) bool { return node.Data == "span" })))
	}
	return entry
}

func presenterNames(n *html.Node) []string {
	seen := map[string]bool{}
	var out []string
	walkHTML(n, func(node *html.Node) bool {
		if node.Type != html.ElementNode || node.Data != "a" || !strings.HasPrefix(htmlAttr(node, "href"), "/prowadzacy/") {
			return true
		}
		name := cleanText(textContent(node))
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		return true
	})
	return out
}

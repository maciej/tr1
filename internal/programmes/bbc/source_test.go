package bbc

import (
	"strings"
	"testing"
	"time"
)

func TestScheduleURLForDate(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)

	got, date := scheduleURLForDate("", now)
	want := "https://rms.api.bbc.co.uk/v2/broadcasts/schedules/bbc_world_service/2026-05-24"
	if got != want {
		t.Fatalf("schedule URL = %q, want %q", got, want)
	}
	if date.Format("2006-01-02") != "2026-05-24" {
		t.Fatalf("schedule date = %s, want 2026-05-24", date.Format("2006-01-02"))
	}

	got, date = scheduleURLForDate("https://example.test/schedules/{date}", now)
	if got != "https://example.test/schedules/2026-05-24" || date.Format("2006-01-02") != "2026-05-24" {
		t.Fatalf("template schedule URL/date = %q %s", got, date.Format("2006-01-02"))
	}

	got, date = scheduleURLForDate("https://example.test/schedules/2026-05-25", now)
	if got != "https://example.test/schedules/2026-05-25" || date.Format("2006-01-02") != "2026-05-25" {
		t.Fatalf("dated schedule URL/date = %q %s", got, date.Format("2006-01-02"))
	}
}

func TestParseSchedule(t *testing.T) {
	data := []byte(`{
  "data": [
    {
      "id": "previous",
      "urn": "urn:bbc:radio:episode:w_previous",
      "start": "2026-05-23T23:32:30Z",
      "end": "2026-05-24T00:00:00Z",
      "duration": 1650,
      "titles": {"primary": "Previous", "secondary": "Yesterday"},
      "container": {"id": "p_previous", "title": "Previous"},
      "image_url": "https://ichef.bbci.co.uk/images/ic/{recipe}/previous.jpg"
    },
    {
      "id": "p0nf8w5y",
      "urn": "urn:bbc:radio:episode:w1730nkmgdxjpp1",
      "start": "2026-05-24T00:00:00Z",
      "end": "2026-05-24T00:06:00Z",
      "duration": 360,
      "titles": {"primary": "BBC News", "secondary": "24/05/2026 00:01 GMT"},
      "container": {"id": "p002vsmz", "title": "BBC News"},
      "image_url": "https://ichef.bbci.co.uk/images/ic/{recipe}/p060dh18.jpg",
      "playable_item": {
        "id": "w1msm0v17k7hq2n",
        "urn": "urn:bbc:radio:episode:w1730nkmgdxjpp1",
        "container": {"id": "p002vsmz", "title": "BBC News"}
      }
    },
    {
      "id": "p0nf8w64",
      "urn": "urn:bbc:radio:episode:w3ct94h2",
      "start": "2026-05-24T00:32:30Z",
      "end": "2026-05-24T00:50:00Z",
      "duration": 1050,
      "titles": {"primary": "Inheritance: Samsung", "secondary": "9. Leverage"},
      "container": {"id": "p0abc123", "title": "Inheritance: Samsung"}
    }
  ]
}`)

	entries, err := ParseSchedule(data, time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ParseSchedule returned error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries length = %d, want 2", len(entries))
	}
	first := entries[0]
	if first.Day != "Sunday" || first.DayIndex != 6 || first.Time != "00:00" {
		t.Fatalf("first entry date fields = %+v", first)
	}
	if first.Programme != "BBC News" || first.Episode != "24/05/2026 00:01 GMT" {
		t.Fatalf("first entry titles = %+v", first)
	}
	if first.ProgrammeURL != "https://www.bbc.co.uk/programmes/p002vsmz" {
		t.Fatalf("programme URL = %q", first.ProgrammeURL)
	}
	if first.EpisodeURL != "https://www.bbc.co.uk/programmes/w1730nkmgdxjpp1" {
		t.Fatalf("episode URL = %q", first.EpisodeURL)
	}
	if first.ImageURL != "https://ichef.bbci.co.uk/images/ic/320x180/p060dh18.jpg" {
		t.Fatalf("image URL = %q", first.ImageURL)
	}
	if first.PodcastID != "p0nf8w5y" || first.Duration != "6 min" {
		t.Fatalf("first entry ids/duration = %+v", first)
	}
	second := entries[1]
	if second.Time != "00:32" || second.Duration != "17 min 30 sec" || !strings.Contains(second.EpisodeURL, "w3ct94h2") {
		t.Fatalf("second entry = %+v", second)
	}
}

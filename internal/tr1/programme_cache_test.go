package tr1

import (
	"testing"
	"time"

	"tr1/internal/programmes"
	"tr1/internal/programmes/bbc"
)

func TestHistoricalBBCScheduleSurvivesCacheAndUsesBroadcastTimestamp(t *testing.T) {
	entries, err := bbc.ParseSchedule([]byte(`{"data":[{"start":"2026-05-24T00:32:30Z","end":"2026-05-24T00:50:00Z","titles":{"primary":"History"}}]}`), time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	store := programmes.NewSQLiteStore(programmes.CachePath(t.TempDir()))
	_, err = store.PutSchedule(t.Context(), programmes.Schedule{Station: "BBC World Service", FetchedAt: "2026-06-10T12:00:00Z", Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := store.LatestSchedule(t.Context(), "BBC World Service")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"segment", "programme"} {
		windows, err := programmeWindows(schedule, time.FixedZone("test", 2*60*60), ProgrammeTranscribeOptions{WindowMode: mode})
		if err != nil {
			t.Fatal(err)
		}
		if len(windows) != 1 {
			t.Fatalf("%s: got %d windows", mode, len(windows))
		}
		if got := windows[0].StartUTC.Format(time.RFC3339); got != "2026-05-24T00:32:30Z" {
			t.Fatalf("%s: start = %s", mode, got)
		}
	}
}

func TestProgrammeAssetsRefreshWithBoundsAndCoverage(t *testing.T) {
	start := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	window := programmeWindow{Station: "tokfm", Entry: programmes.Entry{Programme: "Morning"}, StartLocal: start, StartUTC: start, EndUTC: start.Add(time.Hour), AudioStartUTC: start, AudioEndUTC: start.Add(time.Hour)}
	window.ID = programmeWindowID(window)
	spans := []recordingSpan{{recording: recording{Path: "first.mka", StartUTC: start}, EndUTC: start.Add(30 * time.Minute)}}
	root := t.TempDir()
	paths := func(w programmeWindow, sources []recordingSpan) []string {
		t.Helper()
		covered := windowsWithCoverage([]programmeWindow{w}, sources, time.Minute)
		if len(covered) != 1 {
			t.Fatal("missing covered window")
		}
		w = covered[0]
		audio := programmeAudioPath(root, w, time.UTC)
		return []string{audio, programmeTranscriptPath(root, w, "whisper", Config{Model: "test", Language: "pl"}, time.UTC), programmeDiarizationPath(root, w, time.UTC, "test", "cpu"), programmeDiarizationAudioPath(root, w, time.UTC)}
	}
	original := paths(window, spans)
	unchanged := paths(window, spans)
	for i := range original {
		if unchanged[i] != original[i] {
			t.Fatal("unchanged input missed cache")
		}
	}
	full := append(append([]recordingSpan(nil), spans...), recordingSpan{recording: recording{Path: "second.mka", StartUTC: start.Add(30 * time.Minute)}, EndUTC: start.Add(time.Hour)})
	shifted := window
	shifted.AudioStartUTC = shifted.AudioStartUTC.Add(time.Minute)
	shortened := window
	shortened.AudioEndUTC = shortened.AudioEndUTC.Add(-40 * time.Minute)
	replaced := append([]recordingSpan(nil), spans...)
	replaced[0].Path = "replacement.mka"
	for name, changed := range map[string][]string{"new coverage": paths(window, full), "stream delay": paths(shifted, spans), "end boundary": paths(shortened, spans), "source": paths(window, replaced)} {
		for i := range original {
			if changed[i] == original[i] {
				t.Errorf("%s reused asset %s", name, original[i])
			}
		}
	}
}

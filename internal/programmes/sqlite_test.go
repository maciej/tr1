package programmes

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCachePathUsesCacheRoot(t *testing.T) {
	cacheRoot := t.TempDir()

	got := CachePath(cacheRoot)
	want := filepath.Join(cacheRoot, "programmes", "programmes.sqlite")
	if got != want {
		t.Fatalf("programme cache path = %q, want %q", got, want)
	}
}

func TestSQLiteStoreIgnoresExistingEntries(t *testing.T) {
	cacheRoot := t.TempDir()
	path := CachePath(cacheRoot)
	schedule := Schedule{
		Station:   "TokFM",
		SourceURL: "https://audycje.tokfm.pl/ramowka",
		FetchedAt: "2026-05-21T12:00:00Z",
		Entries: []Entry{
			{
				DayIndex:     3,
				Day:          "Thursday",
				Time:         "07:00",
				Programme:    "Poranek TOK FM",
				ProgrammeURL: "https://audycje.tokfm.pl/audycja/117,Poranek-TOK-FM",
				Episode:      "Czy Ameryka zwija relacje z Polską?",
				EpisodeURL:   "https://audycje.tokfm.pl/podcast/192671,Czy-Ameryka",
				Hosts:        []string{"Dominika Wielowieyska"},
				PodcastID:    "192671",
				Duration:     "13 min",
			},
			{
				DayIndex:     6,
				Day:          "Sunday",
				Time:         "10:00",
				Programme:    "Wiesz, co jesz",
				ProgrammeURL: "https://audycje.tokfm.pl/audycja/685,Wiesz-co-jesz",
				Hosts:        []string{"Patrycja Wanat"},
			},
		},
	}

	first, err := NewSQLiteStore(path).PutSchedule(t.Context(), schedule)
	if err != nil {
		t.Fatalf("first PutSchedule returned error: %v", err)
	}
	if first.NewEntries != 2 {
		t.Fatalf("first cache inserted %d entries, want 2", first.NewEntries)
	}
	second, err := NewSQLiteStore(path).PutSchedule(t.Context(), schedule)
	if err != nil {
		t.Fatalf("second PutSchedule returned error: %v", err)
	}
	if second.NewEntries != 0 {
		t.Fatalf("second cache inserted %d entries, want 0", second.NewEntries)
	}
	if second.Path != first.Path {
		t.Fatalf("cache path changed from %q to %q", first.Path, second.Path)
	}
	if got := countRows(t, first.Path); got != 2 {
		t.Fatalf("cache row count = %d, want 2", got)
	}
}

func TestSQLiteStoreLoadsLatestWholeScheduleSnapshot(t *testing.T) {
	cacheRoot := t.TempDir()
	path := CachePath(cacheRoot)
	store := NewSQLiteStore(path)
	oldSchedule := Schedule{
		Station:   "TokFM",
		SourceURL: "https://audycje.tokfm.pl/ramowka",
		FetchedAt: "2026-05-20T12:00:00Z",
		Entries: []Entry{
			{DayIndex: 0, Day: "Monday", Time: "07:00", Programme: "Old"},
		},
	}
	newSchedule := Schedule{
		Station:   "TokFM",
		SourceURL: "https://audycje.tokfm.pl/ramowka",
		FetchedAt: "2026-05-21T12:00:00Z",
		Entries: []Entry{
			{DayIndex: 1, Day: "Tuesday", Time: "07:00", Programme: "New", Hosts: []string{"Host"}},
			{DayIndex: 1, Day: "Tuesday", Time: "07:20", Programme: "Next"},
		},
	}
	if _, err := store.PutSchedule(t.Context(), oldSchedule); err != nil {
		t.Fatalf("old PutSchedule returned error: %v", err)
	}
	if _, err := store.PutSchedule(t.Context(), newSchedule); err != nil {
		t.Fatalf("new PutSchedule returned error: %v", err)
	}

	got, err := store.LatestSchedule(t.Context(), "TokFM")
	if err != nil {
		t.Fatalf("LatestSchedule returned error: %v", err)
	}
	if got.FetchedAt != newSchedule.FetchedAt {
		t.Fatalf("latest fetched_at = %q, want %q", got.FetchedAt, newSchedule.FetchedAt)
	}
	if len(got.Entries) != len(newSchedule.Entries) {
		t.Fatalf("latest entries = %d, want %d", len(got.Entries), len(newSchedule.Entries))
	}
	if got.Entries[0].Programme != "New" || got.Entries[0].Hosts[0] != "Host" {
		t.Fatalf("latest schedule entry was not restored: %#v", got.Entries[0])
	}
}

func countRows(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM programme_entries`).Scan(&count); err != nil {
		t.Fatalf("count programme entries returned error: %v", err)
	}
	return count
}

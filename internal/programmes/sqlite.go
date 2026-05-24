package programmes

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	path string
}

func CachePath(cacheRoot string) string {
	return filepath.Join(cacheRoot, "programmes", CacheFile)
}

func NewSQLiteStore(path string) *SQLiteStore {
	return &SQLiteStore{path: path}
}

func (s *SQLiteStore) PutSchedule(ctx context.Context, schedule Schedule) (CacheResult, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return CacheResult{}, err
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return CacheResult{}, err
	}
	defer db.Close()
	if err := ensureSchema(ctx, db); err != nil {
		return CacheResult{}, err
	}
	if err := insertSnapshot(ctx, db, schedule); err != nil {
		return CacheResult{}, err
	}
	inserted, err := insertEntries(ctx, db, schedule)
	if err != nil {
		return CacheResult{}, err
	}
	return CacheResult{Path: s.path, NewEntries: inserted}, nil
}

func (s *SQLiteStore) LatestSchedule(ctx context.Context, station string) (Schedule, error) {
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return Schedule{}, err
	}
	defer db.Close()
	if err := ensureSchema(ctx, db); err != nil {
		return Schedule{}, err
	}
	schedule, err := latestSnapshot(ctx, db, station)
	if err == nil {
		return schedule, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, err
	}
	return latestEntriesSchedule(ctx, db, station)
}

func ensureSchema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS programme_entries (
			id INTEGER PRIMARY KEY,
			station TEXT NOT NULL,
			source_url TEXT NOT NULL,
			fetched_at TEXT NOT NULL,
			day_index INTEGER NOT NULL,
			day TEXT NOT NULL,
			time TEXT NOT NULL,
			programme TEXT NOT NULL,
			programme_url TEXT NOT NULL,
			episode TEXT NOT NULL,
			episode_url TEXT NOT NULL,
			hosts_json TEXT NOT NULL,
			image_url TEXT NOT NULL,
			podcast_id TEXT NOT NULL,
			duration TEXT NOT NULL,
			content_hash TEXT NOT NULL UNIQUE
		)`,
		`CREATE TABLE IF NOT EXISTS programme_schedule_snapshots (
			id INTEGER PRIMARY KEY,
			station TEXT NOT NULL,
			source_url TEXT NOT NULL,
			fetched_at TEXT NOT NULL,
			entries_json TEXT NOT NULL,
			content_hash TEXT NOT NULL UNIQUE
		)`,
		`CREATE INDEX IF NOT EXISTS programme_entries_station_day_time_idx ON programme_entries (station, day_index, time)`,
		`CREATE INDEX IF NOT EXISTS programme_schedule_snapshots_station_fetched_idx ON programme_schedule_snapshots (station, fetched_at)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func insertSnapshot(ctx context.Context, db *sql.DB, schedule Schedule) error {
	entriesJSON, err := json.Marshal(schedule.Entries)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT OR IGNORE INTO programme_schedule_snapshots (
		station, source_url, fetched_at, entries_json, content_hash
	) VALUES (?, ?, ?, ?, ?)`,
		schedule.Station,
		schedule.SourceURL,
		schedule.FetchedAt,
		string(entriesJSON),
		scheduleHash(schedule),
	)
	return err
}

func latestSnapshot(ctx context.Context, db *sql.DB, station string) (Schedule, error) {
	var schedule Schedule
	var entriesJSON string
	err := db.QueryRowContext(ctx, `SELECT station, source_url, fetched_at, entries_json
		FROM programme_schedule_snapshots
		WHERE station = ?
		ORDER BY fetched_at DESC, id DESC
		LIMIT 1`, station).Scan(&schedule.Station, &schedule.SourceURL, &schedule.FetchedAt, &entriesJSON)
	if err != nil {
		return Schedule{}, err
	}
	if err := json.Unmarshal([]byte(entriesJSON), &schedule.Entries); err != nil {
		return Schedule{}, err
	}
	return schedule, nil
}

func latestEntriesSchedule(ctx context.Context, db *sql.DB, station string) (Schedule, error) {
	var fetchedAt, sourceURL string
	err := db.QueryRowContext(ctx, `SELECT fetched_at, source_url
		FROM programme_entries
		WHERE station = ?
		ORDER BY fetched_at DESC, id DESC
		LIMIT 1`, station).Scan(&fetchedAt, &sourceURL)
	if err != nil {
		return Schedule{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT
			day_index, day, time, programme, programme_url, episode, episode_url,
			hosts_json, image_url, podcast_id, duration
		FROM programme_entries
		WHERE station = ? AND fetched_at = ?
		ORDER BY day_index, time, id`, station, fetchedAt)
	if err != nil {
		return Schedule{}, err
	}
	defer rows.Close()

	schedule := Schedule{Station: station, SourceURL: sourceURL, FetchedAt: fetchedAt}
	for rows.Next() {
		var entry Entry
		var hostsJSON string
		if err := rows.Scan(
			&entry.DayIndex,
			&entry.Day,
			&entry.Time,
			&entry.Programme,
			&entry.ProgrammeURL,
			&entry.Episode,
			&entry.EpisodeURL,
			&hostsJSON,
			&entry.ImageURL,
			&entry.PodcastID,
			&entry.Duration,
		); err != nil {
			return Schedule{}, err
		}
		if err := json.Unmarshal([]byte(hostsJSON), &entry.Hosts); err != nil {
			return Schedule{}, err
		}
		schedule.Entries = append(schedule.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return Schedule{}, err
	}
	if len(schedule.Entries) == 0 {
		return Schedule{}, sql.ErrNoRows
	}
	return schedule, nil
}

func insertEntries(ctx context.Context, db *sql.DB, schedule Schedule) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO programme_entries (
		station, source_url, fetched_at, day_index, day, time, programme, programme_url,
		episode, episode_url, hosts_json, image_url, podcast_id, duration, content_hash
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var inserted int64
	for _, entry := range schedule.Entries {
		hostsJSON, err := json.Marshal(entry.Hosts)
		if err != nil {
			return 0, err
		}
		result, err := stmt.ExecContext(ctx,
			schedule.Station,
			schedule.SourceURL,
			schedule.FetchedAt,
			entry.DayIndex,
			entry.Day,
			entry.Time,
			entry.Programme,
			entry.ProgrammeURL,
			entry.Episode,
			entry.EpisodeURL,
			string(hostsJSON),
			entry.ImageURL,
			entry.PodcastID,
			entry.Duration,
			entryHash(schedule.Station, entry),
		)
		if err != nil {
			return 0, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		inserted += rows
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}

func entryHash(station string, entry Entry) string {
	data, _ := json.Marshal(struct {
		Station string `json:"station"`
		Entry   Entry  `json:"entry"`
	}{
		Station: station,
		Entry:   entry,
	})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func scheduleHash(schedule Schedule) string {
	data, _ := json.Marshal(struct {
		Station   string  `json:"station"`
		SourceURL string  `json:"source_url"`
		Entries   []Entry `json:"entries"`
	}{
		Station:   schedule.Station,
		SourceURL: schedule.SourceURL,
		Entries:   schedule.Entries,
	})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

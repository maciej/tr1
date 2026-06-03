package tr1

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"tr1/internal/programmes"
)

const (
	programmeAudioCacheV1       = "programme-audio-v1"
	programmeTranscriptCacheV1  = "programme-transcript-v1"
	programmeDiarizationCacheV1 = "programme-diarization-v1"
	programmeDiarizationAudioV1 = "pyannote-audio-v1"
	programmePostprocessV1      = "programme-postprocess-v3"
	defaultProgrammeTimezone    = "Europe/Warsaw"
	defaultProgrammePreRoll     = 0
	defaultProgrammePostRoll    = 0
	defaultProgrammeMinDuration = time.Minute
	defaultProgrammeCodexModel  = "gpt-5.4-mini"
	defaultProgrammeCodexBin    = "codex"
	defaultProgrammePyannotePy  = "python3"
	defaultProgrammePyannote    = "pyannote/speaker-diarization-community-1"
	defaultProgrammePyannoteDev = "auto"
)

type ProgrammeTranscribeOptions struct {
	Schedule          programmes.Schedule
	Station           string
	Timezone          string
	TimezoneSet       bool
	From              string
	To                string
	Limit             int
	StreamDelay       time.Duration
	PreRoll           time.Duration
	PostRoll          time.Duration
	MinDuration       time.Duration
	TranscribeChunk   time.Duration
	WindowMode        string
	PriorityOnly      bool
	Diarize           bool
	SpeakerMap        bool
	PlanOnly          bool
	CodexBin          string
	CodexModel        string
	PyannotePythonBin string
	PyannoteModel     string
	PyannoteDevice    string
	Force             bool
}

type programmeWindow struct {
	Entry          programmes.Entry
	ID             string
	Station        string
	StartLocal     time.Time
	EndLocal       time.Time
	StartUTC       time.Time
	EndUTC         time.Time
	AudioStartUTC  time.Time
	AudioEndUTC    time.Time
	DurationHint   time.Duration
	Priority       bool
	CoverageStart  time.Time
	CoverageEnd    time.Time
	CoverageStatus string
}

type recordingSpan struct {
	recording
	Duration time.Duration
	EndUTC   time.Time
}

type programmeTranscript struct {
	Version     string                    `json:"version"`
	Created     string                    `json:"created_at"`
	Backend     string                    `json:"backend"`
	Model       string                    `json:"model"`
	Language    string                    `json:"language"`
	Programme   programmeTranscriptMeta   `json:"programme"`
	Alignment   programmeAlignmentMeta    `json:"alignment"`
	Audio       programmeAudioMeta        `json:"audio"`
	Whisper     whisperOutput             `json:"whisper"`
	Diarization *programmeDiarizationMeta `json:"diarization,omitempty"`
	SpeakerMap  *programmeSpeakerMapMeta  `json:"speaker_map,omitempty"`
	Postprocess *programmePostprocessMeta `json:"postprocess,omitempty"`
	Markdown    string                    `json:"markdown"`
}

type programmeTranscriptMeta struct {
	Station      string   `json:"station"`
	Programme    string   `json:"programme"`
	Episode      string   `json:"episode,omitempty"`
	Hosts        []string `json:"hosts,omitempty"`
	PodcastID    string   `json:"podcast_id,omitempty"`
	ProgrammeURL string   `json:"programme_url,omitempty"`
	EpisodeURL   string   `json:"episode_url,omitempty"`
	StartLocal   string   `json:"start_local"`
	EndLocal     string   `json:"end_local"`
	StartUTC     string   `json:"start_utc"`
	EndUTC       string   `json:"end_utc"`
	Priority     bool     `json:"priority"`
}

type programmeAlignmentMeta struct {
	StreamDelaySeconds float64 `json:"stream_delay_seconds"`
	PreRollSeconds     float64 `json:"pre_roll_seconds"`
	PostRollSeconds    float64 `json:"post_roll_seconds"`
	Confidence         string  `json:"confidence"`
	Notes              string  `json:"notes"`
}

type programmeAudioMeta struct {
	Path          string   `json:"path"`
	StartUTC      string   `json:"start_utc"`
	EndUTC        string   `json:"end_utc"`
	Duration      float64  `json:"duration_seconds"`
	SourceChunks  []string `json:"source_chunks"`
	CacheVersion  string   `json:"cache_version"`
	CoverageStart string   `json:"coverage_start_utc,omitempty"`
	CoverageEnd   string   `json:"coverage_end_utc,omitempty"`
}

type programmeDiarizationMeta struct {
	Version  string               `json:"version"`
	Path     string               `json:"path"`
	Engine   string               `json:"engine"`
	Segments []diarizationSegment `json:"segments"`
	Error    string               `json:"error,omitempty"`
}

type programmeSpeakerMapMeta struct {
	Model   string            `json:"model"`
	Mapping map[string]string `json:"mapping,omitempty"`
	Raw     string            `json:"raw_response,omitempty"`
	Error   string            `json:"error,omitempty"`
}

type programmePostprocessMeta struct {
	Version      string                    `json:"version"`
	Model        string                    `json:"model"`
	Removed      []programmeRemovedBlock   `json:"removed_blocks,omitempty"`
	Rewritten    []programmeRewrittenBlock `json:"rewritten_blocks,omitempty"`
	RawResponses []string                  `json:"raw_responses,omitempty"`
	Error        string                    `json:"error,omitempty"`
}

type programmeRemovedBlock struct {
	ID      int     `json:"id"`
	Speaker string  `json:"speaker,omitempty"`
	Start   float64 `json:"start,omitempty"`
	End     float64 `json:"end,omitempty"`
	Text    string  `json:"text,omitempty"`
	Reason  string  `json:"reason,omitempty"`
}

type programmeRewrittenBlock struct {
	ID       int     `json:"id"`
	Speaker  string  `json:"speaker,omitempty"`
	Start    float64 `json:"start,omitempty"`
	End      float64 `json:"end,omitempty"`
	Original string  `json:"original,omitempty"`
	Text     string  `json:"text,omitempty"`
	Reason   string  `json:"reason,omitempty"`
}

type diarizationSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
}

type diarizedParagraph struct {
	Speaker string
	Text    string
	Start   float64
	End     float64
}

type programmeMarkdownParagraph struct {
	ID      int
	Speaker string
	Text    string
	Start   float64
	End     float64
}

func DefaultProgrammeTranscribeOptions() ProgrammeTranscribeOptions {
	return ProgrammeTranscribeOptions{
		Station:           defaultStationAlias,
		Timezone:          defaultProgrammeTimezone,
		PreRoll:           defaultProgrammePreRoll,
		PostRoll:          defaultProgrammePostRoll,
		MinDuration:       defaultProgrammeMinDuration,
		TranscribeChunk:   4 * time.Minute,
		WindowMode:        "programme",
		Diarize:           true,
		SpeakerMap:        true,
		CodexBin:          defaultProgrammeCodexBin,
		CodexModel:        defaultProgrammeCodexModel,
		PyannotePythonBin: defaultProgrammePyannotePy,
		PyannoteModel:     defaultProgrammePyannote,
		PyannoteDevice:    defaultProgrammePyannoteDev,
	}
}

func DefaultProgrammeTimezoneForStation(station string) string {
	selected, err := lookupStation(station)
	if err == nil && selected.Name == "BBC World Service" {
		return "UTC"
	}
	return defaultProgrammeTimezone
}

func RunProgrammeTranscribe(w io.Writer, ctx context.Context, cfg Config, opts ProgrammeTranscribeOptions) error {
	return runProgrammeTranscribe(w, ctx, cfg, opts)
}

func ValidateProgrammeTranscribeConfig(cfg Config, opts ProgrammeTranscribeOptions) error {
	if err := validateRecordTranscribeConfig(cfg); err != nil {
		return err
	}
	if opts.Limit < 0 {
		return fmt.Errorf("--limit must be >= 0")
	}
	if opts.MinDuration <= 0 {
		return fmt.Errorf("--min-duration must be greater than 0")
	}
	if opts.TranscribeChunk < 0 {
		return fmt.Errorf("--transcribe-chunk-duration must be >= 0")
	}
	switch strings.ToLower(strings.TrimSpace(opts.WindowMode)) {
	case "", "programme", "segment":
	default:
		return fmt.Errorf("--window-mode must be one of: programme, segment")
	}
	if opts.Schedule.Station == "" || len(opts.Schedule.Entries) == 0 {
		return fmt.Errorf("programme schedule is empty")
	}
	if opts.Diarize && opts.PyannotePythonBin == "" {
		return fmt.Errorf("--pyannote-python-bin must not be empty")
	}
	if opts.Diarize && strings.TrimSpace(opts.PyannoteModel) == "" {
		return fmt.Errorf("--pyannote-model must not be empty")
	}
	switch strings.ToLower(strings.TrimSpace(opts.PyannoteDevice)) {
	case "", "auto", "cpu", "mps", "cuda":
	default:
		return fmt.Errorf("--pyannote-device must be one of: auto, cpu, mps, cuda")
	}
	if opts.SpeakerMap && opts.CodexBin == "" {
		return fmt.Errorf("--codex-bin must not be empty")
	}
	return nil
}

func runProgrammeTranscribe(w io.Writer, ctx context.Context, cfg Config, opts ProgrammeTranscribeOptions) error {
	if opts.Station == "" {
		opts.Station = cfg.Station
	}
	selected, err := lookupStation(opts.Station)
	if err != nil {
		return err
	}
	if opts.Timezone == "" || (!opts.TimezoneSet && opts.Timezone == defaultProgrammeTimezone) {
		opts.Timezone = DefaultProgrammeTimezoneForStation(opts.Station)
	}
	if opts.CodexBin == "" {
		opts.CodexBin = defaultProgrammeCodexBin
	}
	if opts.CodexModel == "" {
		opts.CodexModel = defaultProgrammeCodexModel
	}
	if opts.PyannotePythonBin == "" {
		opts.PyannotePythonBin = defaultProgrammePyannotePy
	}
	if opts.PyannoteModel == "" {
		opts.PyannoteModel = defaultProgrammePyannote
	}
	if opts.PyannoteDevice == "" {
		opts.PyannoteDevice = defaultProgrammePyannoteDev
	}
	if opts.MinDuration == 0 {
		opts.MinDuration = defaultProgrammeMinDuration
	}
	if err := ValidateProgrammeTranscribeConfig(cfg, opts); err != nil {
		return err
	}
	cacheRoot, err := recordingCacheRoot(cfg)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(opts.Timezone)
	if err != nil {
		return err
	}
	from, to, err := programmeTimeBounds(opts, loc)
	if err != nil {
		return err
	}
	windows, err := programmeWindows(opts.Schedule, loc, opts)
	if err != nil {
		return err
	}
	windows = filterProgrammeWindows(windows, from, to, opts)

	recordings, err := listRecordings(cacheRoot, opts.Station)
	if err != nil {
		return err
	}
	spans, err := recordingSpans(ctx, cfg, recordings)
	if err != nil {
		return err
	}
	windows = windowsWithCoverage(windows, spans, opts.MinDuration)
	if opts.Limit > 0 && len(windows) > opts.Limit {
		windows = windows[:opts.Limit]
	}
	if len(windows) == 0 {
		return writeProgrammeTranscriptSummary(w, nil)
	}
	if opts.PlanOnly {
		rows := make([]programmeSummaryRow, 0, len(windows))
		for _, window := range windows {
			rows = append(rows, programmeSummaryRow{Status: "planned", Window: window})
		}
		return writeProgrammeTranscriptSummary(w, rows)
	}

	if err := ensureDirs(cfg); err != nil {
		return err
	}
	backend, err := prepareBackend(ctx, &cfg)
	if err != nil {
		return err
	}
	applyStationLanguageDefault(&cfg, selected)
	var rows []programmeSummaryRow
	for _, window := range windows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		audioPath := programmeAudioPath(cacheRoot, window, loc)
		sourceChunks := overlappingRecordingPaths(spans, window.AudioStartUTC, window.AudioEndUTC)
		if len(sourceChunks) == 0 {
			rows = append(rows, programmeSummaryRow{Status: "skipped", Window: window, Path: "", Note: "no cached audio"})
			continue
		}
		if opts.Force || !fileExists(audioPath) {
			status(cfg, "programme-audio", fmt.Sprintf("cutting %s", windowDisplayName(window)))
			if err := cutProgrammeAudio(ctx, cfg, spans, window.AudioStartUTC, window.AudioEndUTC, audioPath); err != nil {
				return err
			}
		}
		jsonPath := programmeTranscriptPath(cacheRoot, window, backend, cfg, loc)
		if !opts.Force && fileExists(jsonPath) {
			if opts.Diarize || opts.SpeakerMap {
				updated, err := augmentCachedProgrammeTranscript(ctx, cfg, opts, cacheRoot, window, audioPath, jsonPath, loc)
				if err != nil {
					return err
				}
				if updated {
					rows = append(rows, programmeSummaryRow{Status: "updated", Window: window, Path: jsonPath})
					continue
				}
			}
			rows = append(rows, programmeSummaryRow{Status: "cached", Window: window, Path: jsonPath})
			continue
		}
		status(cfg, "programme", fmt.Sprintf("transcribing %s", windowDisplayName(window)))
		out, err := transcribeProgrammeAudio(ctx, cfg, cfg.Model, audioPath, window.AudioEndUTC.Sub(window.AudioStartUTC), opts.TranscribeChunk, opts.Force)
		if err != nil {
			return err
		}
		transcript := programmeTranscript{
			Version:   programmeTranscriptCacheV1,
			Created:   time.Now().UTC().Format(time.RFC3339Nano),
			Backend:   backend,
			Model:     cfg.Model,
			Language:  cfg.Language,
			Programme: programmeTranscriptMetaFromWindow(window),
			Alignment: programmeAlignmentMeta{
				StreamDelaySeconds: opts.StreamDelay.Seconds(),
				PreRollSeconds:     opts.PreRoll.Seconds(),
				PostRollSeconds:    opts.PostRoll.Seconds(),
				Confidence:         "medium",
				Notes:              "Delay is explicit metadata. Use --stream-delay when the captured stream is known to lag or lead the published schedule.",
			},
			Audio: programmeAudioMeta{
				Path:          audioPath,
				StartUTC:      window.AudioStartUTC.Format(time.RFC3339),
				EndUTC:        window.AudioEndUTC.Format(time.RFC3339),
				Duration:      window.AudioEndUTC.Sub(window.AudioStartUTC).Seconds(),
				SourceChunks:  sourceChunks,
				CacheVersion:  programmeAudioCacheV1,
				CoverageStart: window.CoverageStart.Format(time.RFC3339),
				CoverageEnd:   window.CoverageEnd.Format(time.RFC3339),
			},
			Whisper: out,
		}
		var diarized []diarizedParagraph
		if opts.Diarize {
			diarization, err := ensureProgrammeDiarization(ctx, cfg, opts, cacheRoot, window, audioPath, loc)
			transcript.Diarization = &diarization
			if err == nil && len(diarization.Segments) > 0 {
				diarized = diarizedTranscript(out, diarization.Segments)
			} else if err != nil {
				transcript.Diarization.Error = err.Error()
				_ = writeProgrammeTranscript(jsonPath, transcript)
				return err
			}
		}
		if opts.SpeakerMap && len(diarized) > 0 {
			speakerMap, err := mapProgrammeSpeakers(ctx, opts, window, diarized)
			transcript.SpeakerMap = &speakerMap
			if err != nil {
				_ = writeProgrammeTranscript(jsonPath, transcript)
				return err
			}
		}
		transcript.Markdown = programmeMarkdown(window, out, diarized, transcript.SpeakerMap)
		if opts.SpeakerMap && len(diarized) > 0 {
			postprocess, markdown, err := postprocessProgrammeMarkdown(ctx, opts, window, diarized, transcript.SpeakerMap)
			transcript.Postprocess = &postprocess
			transcript.Markdown = markdown
			if err != nil {
				_ = writeProgrammeTranscript(jsonPath, transcript)
				return err
			}
		}
		if err := writeProgrammeTranscript(jsonPath, transcript); err != nil {
			return err
		}
		if err := writeProgrammeMarkdown(strings.TrimSuffix(jsonPath, ".json")+".md", transcript.Markdown); err != nil {
			return err
		}
		rows = append(rows, programmeSummaryRow{Status: "transcribed", Window: window, Path: jsonPath})
	}
	return writeProgrammeTranscriptSummary(w, rows)
}

func applyStationLanguageDefault(cfg *Config, station Station) {
	if cfg.LanguageSet {
		return
	}
	if station.Language != "" {
		cfg.Language = station.Language
	}
}

func programmeTimeBounds(opts ProgrammeTranscribeOptions, loc *time.Location) (time.Time, time.Time, error) {
	from, err := parseProgrammeBound(opts.From, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := parseProgrammeBound(opts.To, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("--from must be before --to")
	}
	return from, to, nil
}

func parseProgrammeBound(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	layouts := []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("could not parse time %q; use RFC3339, YYYY-MM-DD, or YYYY-MM-DD HH:MM", raw)
}

func programmeWindows(schedule programmes.Schedule, loc *time.Location, opts ProgrammeTranscribeOptions) ([]programmeWindow, error) {
	fetchedAt, err := time.Parse(time.RFC3339, schedule.FetchedAt)
	if err != nil {
		return nil, fmt.Errorf("schedule fetched_at %q is not RFC3339: %w", schedule.FetchedAt, err)
	}
	fetchedLocal := fetchedAt.In(loc)
	weekMonday := midnightInLocation(fetchedLocal.AddDate(0, 0, -weekdayIndex(fetchedLocal.Weekday())), loc)
	var entries []datedProgrammeEntry
	for _, entry := range schedule.Entries {
		if entry.DayIndex < 0 || entry.DayIndex > 6 {
			continue
		}
		hour, minute, ok := parseClock(entry.Time)
		if !ok {
			continue
		}
		day := weekMonday.AddDate(0, 0, entry.DayIndex)
		start := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
		entries = append(entries, datedProgrammeEntry{entry: entry, startLocal: start})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].startLocal.Before(entries[j].startLocal)
	})
	if strings.EqualFold(strings.TrimSpace(opts.WindowMode), "segment") {
		return segmentProgrammeWindows(schedule, entries, opts), nil
	}
	return groupedProgrammeWindows(schedule, entries, opts), nil
}

type datedProgrammeEntry struct {
	entry      programmes.Entry
	startLocal time.Time
}

type programmeBlock struct {
	key     string
	entries []datedProgrammeEntry
}

func segmentProgrammeWindows(schedule programmes.Schedule, entries []datedProgrammeEntry, opts ProgrammeTranscribeOptions) []programmeWindow {
	var windows []programmeWindow
	for i, item := range entries {
		nextStart := time.Time{}
		if i+1 < len(entries) {
			nextStart = entries[i+1].startLocal
		}
		durationHint := parseProgrammeDuration(item.entry.Duration)
		end := programmeEnd(item.startLocal, nextStart, durationHint)
		if !end.After(item.startLocal) {
			continue
		}
		startUTC := item.startLocal.UTC()
		endUTC := end.UTC()
		audioStart := startUTC.Add(opts.StreamDelay).Add(-opts.PreRoll)
		audioEnd := endUTC.Add(opts.StreamDelay).Add(opts.PostRoll)
		if !audioEnd.After(audioStart) {
			continue
		}
		window := programmeWindow{
			Entry:         item.entry,
			Station:       stationSlug(schedule.Station),
			StartLocal:    item.startLocal,
			EndLocal:      end,
			StartUTC:      startUTC,
			EndUTC:        endUTC,
			AudioStartUTC: audioStart,
			AudioEndUTC:   audioEnd,
			DurationHint:  durationHint,
			Priority:      isPriorityProgramme(item.entry),
		}
		window.ID = programmeWindowID(window)
		windows = append(windows, window)
	}
	return windows
}

func groupedProgrammeWindows(schedule programmes.Schedule, entries []datedProgrammeEntry, opts ProgrammeTranscribeOptions) []programmeWindow {
	if len(entries) == 0 {
		return nil
	}
	var blocks []programmeBlock
	for _, item := range entries {
		key := programmeFamily(item.entry.Programme)
		if key == "" {
			key = strings.TrimSpace(item.entry.Programme)
		}
		if len(blocks) == 0 {
			blocks = append(blocks, programmeBlock{key: key, entries: []datedProgrammeEntry{item}})
			continue
		}
		lastBlock := &blocks[len(blocks)-1]
		lastEntry := lastBlock.entries[len(lastBlock.entries)-1]
		if lastBlock.key == key && item.startLocal.Sub(lastEntry.startLocal) <= 45*time.Minute {
			lastBlock.entries = append(lastBlock.entries, item)
			continue
		}
		blocks = append(blocks, programmeBlock{key: key, entries: []datedProgrammeEntry{item}})
	}

	var windows []programmeWindow
	for i, b := range blocks {
		first := b.entries[0]
		nextStart := time.Time{}
		if i+1 < len(blocks) {
			nextStart = blocks[i+1].entries[0].startLocal
		}
		end := nextStart
		if end.IsZero() || !end.After(first.startLocal) {
			end = blockHintedEnd(b)
		}
		if !end.After(first.startLocal) {
			continue
		}
		entry := combinedProgrammeEntry(b.key, b.entries)
		startUTC := first.startLocal.UTC()
		endUTC := end.UTC()
		audioStart := startUTC.Add(opts.StreamDelay).Add(-opts.PreRoll)
		audioEnd := endUTC.Add(opts.StreamDelay).Add(opts.PostRoll)
		window := programmeWindow{
			Entry:         entry,
			Station:       stationSlug(schedule.Station),
			StartLocal:    first.startLocal,
			EndLocal:      end,
			StartUTC:      startUTC,
			EndUTC:        endUTC,
			AudioStartUTC: audioStart,
			AudioEndUTC:   audioEnd,
			Priority:      blockPriority(b.entries),
		}
		window.ID = programmeWindowID(window)
		windows = append(windows, window)
	}
	return windows
}

func programmeEnd(start, nextStart time.Time, durationHint time.Duration) time.Time {
	if !nextStart.IsZero() && nextStart.After(start) {
		end := nextStart
		if durationHint > 0 {
			hinted := start.Add(durationHint)
			if hinted.Before(nextStart) && nextStart.Sub(hinted) > 10*time.Minute {
				end = hinted
			}
		}
		return end
	}
	if durationHint > 0 {
		return start.Add(durationHint)
	}
	return start.Add(time.Hour)
}

func blockHintedEnd(b programmeBlock) time.Time {
	end := time.Time{}
	for _, item := range b.entries {
		hint := parseProgrammeDuration(item.entry.Duration)
		if hint <= 0 {
			continue
		}
		candidate := item.startLocal.Add(hint)
		if candidate.After(end) {
			end = candidate
		}
	}
	if end.IsZero() {
		last := b.entries[len(b.entries)-1]
		end = last.startLocal.Add(time.Hour)
	}
	return end
}

func combinedProgrammeEntry(key string, entries []datedProgrammeEntry) programmes.Entry {
	first := entries[0].entry
	first.Programme = key
	if len(entries) > 1 {
		first.Episode = ""
		first.EpisodeURL = ""
		first.PodcastID = ""
		first.Duration = ""
	}
	first.Hosts = uniqueHosts(entries)
	return first
}

func uniqueHosts(entries []datedProgrammeEntry) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, item := range entries {
		for _, host := range item.entry.Hosts {
			host = strings.TrimSpace(host)
			if host == "" || seen[host] {
				continue
			}
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func blockPriority(entries []datedProgrammeEntry) bool {
	for _, item := range entries {
		if isPriorityProgramme(item.entry) {
			return true
		}
	}
	return false
}

func programmeFamily(programme string) string {
	name := strings.TrimSpace(programme)
	normalized := strings.ToLower(removePolishDiacritics(name))
	switch {
	case strings.HasPrefix(normalized, "poranek tok fm"):
		return "Poranek TOK FM"
	case strings.HasPrefix(normalized, "pierwszy program"):
		return "Pierwszy Program"
	case strings.HasPrefix(normalized, "popoludnie tok fm"):
		return "Popołudnie TOK FM"
	case strings.HasPrefix(normalized, "tok360"):
		return "TOK360 - Podsumowanie Dnia"
	default:
		return name
	}
}

func filterProgrammeWindows(windows []programmeWindow, from, to time.Time, opts ProgrammeTranscribeOptions) []programmeWindow {
	var out []programmeWindow
	for _, window := range windows {
		if opts.PriorityOnly && !window.Priority {
			continue
		}
		if !from.IsZero() && !window.EndUTC.After(from) {
			continue
		}
		if !to.IsZero() && !window.StartUTC.Before(to) {
			continue
		}
		out = append(out, window)
	}
	return out
}

func windowsWithCoverage(windows []programmeWindow, spans []recordingSpan, minDuration time.Duration) []programmeWindow {
	var out []programmeWindow
	for _, window := range windows {
		originalStart := window.AudioStartUTC
		originalEnd := window.AudioEndUTC
		start, end, ok := coverageForWindow(spans, window.AudioStartUTC, window.AudioEndUTC)
		if !ok || end.Sub(start) < minDuration {
			continue
		}
		if start.After(window.AudioStartUTC) {
			window.AudioStartUTC = start
		}
		if end.Before(window.AudioEndUTC) {
			window.AudioEndUTC = end
		}
		window.CoverageStart = start
		window.CoverageEnd = end
		if start.Equal(originalStart) && end.Equal(originalEnd) {
			window.CoverageStatus = "full"
		} else {
			window.CoverageStatus = "partial"
		}
		out = append(out, window)
	}
	return out
}

func coverageForWindow(spans []recordingSpan, start, end time.Time) (time.Time, time.Time, bool) {
	coverageStart := time.Time{}
	coverageEnd := time.Time{}
	for _, span := range spans {
		if !span.EndUTC.After(start) || !span.StartUTC.Before(end) {
			continue
		}
		partStart := maxTime(start, span.StartUTC)
		partEnd := minTime(end, span.EndUTC)
		if coverageStart.IsZero() {
			coverageStart = partStart
			coverageEnd = partEnd
			continue
		}
		if partStart.After(coverageEnd.Add(2 * time.Second)) {
			break
		}
		if partEnd.After(coverageEnd) {
			coverageEnd = partEnd
		}
	}
	if coverageStart.IsZero() || !coverageEnd.After(coverageStart) {
		return time.Time{}, time.Time{}, false
	}
	return coverageStart, coverageEnd, true
}

func recordingSpans(ctx context.Context, cfg Config, recordings []recording) ([]recordingSpan, error) {
	var spans []recordingSpan
	for _, rec := range recordings {
		duration, err := mediaDuration(ctx, cfg, rec.Path)
		if err != nil {
			return nil, err
		}
		if duration <= 0 {
			continue
		}
		spans = append(spans, recordingSpan{
			recording: rec,
			Duration:  duration,
			EndUTC:    rec.StartUTC.Add(duration),
		})
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].StartUTC.Before(spans[j].StartUTC)
	})
	return spans, nil
}

func mediaDuration(ctx context.Context, cfg Config, path string) (time.Duration, error) {
	ffprobe := "ffprobe"
	if cfg.FFmpegBin != "" {
		if dir := filepath.Dir(cfg.FFmpegBin); dir != "." && dir != "" {
			candidate := filepath.Join(dir, "ffprobe")
			if _, err := os.Stat(candidate); err == nil {
				ffprobe = candidate
			}
		}
	}
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("ffprobe failed for %s: %w: %s", filepath.Base(path), err, strings.TrimSpace(stderr.String()))
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(stdout.String()), 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration for %s is not a number: %w", filepath.Base(path), err)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func cutProgrammeAudio(ctx context.Context, cfg Config, spans []recordingSpan, start, end time.Time, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	selected := overlappingSpans(spans, start, end)
	if len(selected) == 0 {
		return fmt.Errorf("no cached recordings overlap %s - %s", start.Format(time.RFC3339), end.Format(time.RFC3339))
	}
	listPath := outPath + ".concat.txt"
	listFile, err := os.Create(listPath)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(listFile)
	for _, span := range selected {
		if _, err := fmt.Fprintf(writer, "file '%s'\n", ffconcatEscape(span.Path)); err != nil {
			_ = listFile.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = listFile.Close()
		return err
	}
	if err := listFile.Close(); err != nil {
		return err
	}
	defer os.Remove(listPath)

	base := selected[0].StartUTC
	offset := start.Sub(base).Seconds()
	if offset < 0 {
		offset = 0
	}
	duration := end.Sub(start).Seconds()
	tmp := outPath + ".tmp" + defaultRecordExt
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "concat", "-safe", "0",
		"-i", listPath,
		"-ss", fmt.Sprintf("%.3f", offset),
		"-t", fmt.Sprintf("%.3f", duration),
		"-map", "0:a:0",
		"-vn", "-sn", "-dn",
		"-c:a", "copy",
		"-f", "matroska",
		tmp,
	}
	cmd := exec.CommandContext(ctx, cfg.FFmpegBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg programme cut failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return os.Rename(tmp, outPath)
}

func transcribeProgrammeAudio(ctx context.Context, cfg Config, model, audioPath string, duration, chunkDuration time.Duration, force bool) (whisperOutput, error) {
	if chunkDuration <= 0 || duration <= chunkDuration+time.Minute {
		return transcribe(ctx, cfg, model, audioPath)
	}
	chunkDir := filepath.Join(
		filepath.Dir(audioPath),
		fmt.Sprintf("%s-chunks-%ds", strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath)), int(chunkDuration.Round(time.Second).Seconds())),
	)
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return whisperOutput{}, err
	}
	var combined whisperOutput
	totalChunks := int((duration + chunkDuration - 1) / chunkDuration)
	for index, offset := 0, time.Duration(0); offset < duration; index, offset = index+1, offset+chunkDuration {
		length := chunkDuration
		if remaining := duration - offset; remaining < length {
			length = remaining
		}
		if length <= 0 {
			break
		}
		chunkPath := filepath.Join(chunkDir, fmt.Sprintf("chunk-%06d%s", index, defaultRecordExt))
		if force || !fileExists(chunkPath) {
			if err := cutAudioChunk(ctx, cfg, audioPath, offset, length, chunkPath); err != nil {
				return whisperOutput{}, err
			}
		}
		status(cfg, "programme", fmt.Sprintf("transcribing chunk %d/%d of %s", index+1, totalChunks, filepath.Base(audioPath)))
		out, err := transcribe(ctx, cfg, model, chunkPath)
		if err != nil {
			return whisperOutput{}, err
		}
		combined = appendWhisperOutput(combined, out, offset.Seconds())
	}
	combined.Text = strings.TrimSpace(combined.Text)
	return combined, nil
}

func cutAudioChunk(ctx context.Context, cfg Config, audioPath string, offset, duration time.Duration, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	tmp := outPath + ".tmp" + defaultRecordExt
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-ss", fmt.Sprintf("%.3f", offset.Seconds()),
		"-t", fmt.Sprintf("%.3f", duration.Seconds()),
		"-i", audioPath,
		"-map", "0:a:0",
		"-vn", "-sn", "-dn",
		"-c:a", "copy",
		"-f", "matroska",
		tmp,
	}
	cmd := exec.CommandContext(ctx, cfg.FFmpegBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg chunk cut failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return os.Rename(tmp, outPath)
}

func appendWhisperOutput(dst, src whisperOutput, offsetSeconds float64) whisperOutput {
	if strings.TrimSpace(src.Text) != "" {
		dst.Text = strings.TrimSpace(strings.TrimSpace(dst.Text) + " " + strings.TrimSpace(src.Text))
	}
	for _, segment := range src.Segments {
		adjusted := whisperSegment{Text: segment.Text}
		for _, word := range segment.Words {
			word.Start += offsetSeconds
			word.End += offsetSeconds
			adjusted.Words = append(adjusted.Words, word)
		}
		dst.Segments = append(dst.Segments, adjusted)
	}
	return dst
}

func overlappingSpans(spans []recordingSpan, start, end time.Time) []recordingSpan {
	var out []recordingSpan
	for _, span := range spans {
		if span.EndUTC.After(start) && span.StartUTC.Before(end) {
			out = append(out, span)
		}
	}
	return out
}

func ffconcatEscape(path string) string {
	return strings.ReplaceAll(path, "'", "'\\''")
}

func overlappingRecordingPaths(spans []recordingSpan, start, end time.Time) []string {
	selected := overlappingSpans(spans, start, end)
	out := make([]string, 0, len(selected))
	for _, span := range selected {
		out = append(out, span.Path)
	}
	return out
}

func ensureProgrammeDiarization(ctx context.Context, cfg Config, opts ProgrammeTranscribeOptions, cacheRoot string, window programmeWindow, audioPath string, loc *time.Location) (programmeDiarizationMeta, error) {
	device, err := resolvePyannoteDevice(ctx, opts.PyannotePythonBin, opts.PyannoteDevice)
	if err != nil {
		return programmeDiarizationMeta{}, err
	}
	path := programmeDiarizationPath(cacheRoot, window, loc, opts.PyannoteModel, device)
	meta := programmeDiarizationMeta{
		Version: programmeDiarizationCacheV1,
		Path:    path,
		Engine:  programmePyannoteEngine(opts.PyannoteModel, device),
	}
	if !opts.Force && fileExists(path) {
		segments, err := readDiarization(path)
		if err != nil {
			return meta, err
		}
		meta.Segments = segments
		return meta, nil
	}
	pyannoteAudioPath := programmeDiarizationAudioPath(cacheRoot, window, loc)
	if opts.Force || !fileExists(pyannoteAudioPath) {
		if err := convertAudioForPyannote(ctx, cfg, audioPath, pyannoteAudioPath); err != nil {
			return meta, err
		}
	}
	segments, err := runPyannote(ctx, opts.PyannotePythonBin, opts.PyannoteModel, device, pyannoteAudioPath, path)
	if err != nil {
		return meta, err
	}
	meta.Segments = segments
	return meta, nil
}

func programmePyannoteEngine(modelName, device string) string {
	return "pyannote.audio " + modelName + " device=" + device
}

func augmentCachedProgrammeTranscript(ctx context.Context, cfg Config, opts ProgrammeTranscribeOptions, cacheRoot string, window programmeWindow, audioPath, jsonPath string, loc *time.Location) (bool, error) {
	transcript, err := readProgrammeTranscript(jsonPath)
	if err != nil {
		return false, err
	}
	updated := false
	var diarized []diarizedParagraph
	device := strings.ToLower(strings.TrimSpace(opts.PyannoteDevice))
	if device == "" {
		device = defaultProgrammePyannoteDev
	}
	if device == "auto" {
		var err error
		device, err = resolvePyannoteDevice(ctx, opts.PyannotePythonBin, opts.PyannoteDevice)
		if err != nil {
			return false, err
		}
	}
	if transcript.Diarization != nil && transcript.Diarization.Engine == programmePyannoteEngine(opts.PyannoteModel, device) && len(transcript.Diarization.Segments) > 0 {
		diarized = diarizedTranscript(transcript.Whisper, transcript.Diarization.Segments)
	}
	if opts.Diarize && len(diarized) == 0 {
		diarization, err := ensureProgrammeDiarization(ctx, cfg, opts, cacheRoot, window, audioPath, loc)
		transcript.Diarization = &diarization
		if err != nil {
			transcript.Diarization.Error = err.Error()
			if writeErr := writeProgrammeTranscript(jsonPath, transcript); writeErr != nil {
				return false, writeErr
			}
			return true, err
		}
		diarized = diarizedTranscript(transcript.Whisper, diarization.Segments)
		updated = true
	}
	if opts.SpeakerMap && len(diarized) > 0 && (transcript.SpeakerMap == nil || len(transcript.SpeakerMap.Mapping) == 0) {
		speakerMap, err := mapProgrammeSpeakers(ctx, opts, window, diarized)
		transcript.SpeakerMap = &speakerMap
		updated = true
		if err != nil {
			if writeErr := writeProgrammeTranscript(jsonPath, transcript); writeErr != nil {
				return false, writeErr
			}
			return true, err
		}
	}
	if opts.SpeakerMap && len(diarized) > 0 && needsProgrammePostprocess(transcript.Postprocess, opts) {
		postprocess, markdown, err := postprocessProgrammeMarkdown(ctx, opts, window, diarized, transcript.SpeakerMap)
		transcript.Postprocess = &postprocess
		transcript.Markdown = markdown
		updated = true
		if err != nil {
			if writeErr := writeProgrammeTranscript(jsonPath, transcript); writeErr != nil {
				return false, writeErr
			}
			if writeErr := writeProgrammeMarkdown(strings.TrimSuffix(jsonPath, ".json")+".md", transcript.Markdown); writeErr != nil {
				return false, writeErr
			}
			return true, err
		}
	}
	if updated {
		if transcript.Postprocess == nil {
			transcript.Markdown = programmeMarkdown(window, transcript.Whisper, diarized, transcript.SpeakerMap)
		}
		if err := writeProgrammeTranscript(jsonPath, transcript); err != nil {
			return false, err
		}
		if err := writeProgrammeMarkdown(strings.TrimSuffix(jsonPath, ".json")+".md", transcript.Markdown); err != nil {
			return false, err
		}
	}
	return updated, nil
}

func readProgrammeTranscript(path string) (programmeTranscript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return programmeTranscript{}, err
	}
	var transcript programmeTranscript
	if err := json.Unmarshal(data, &transcript); err != nil {
		return programmeTranscript{}, err
	}
	return transcript, nil
}

func convertAudioForPyannote(ctx context.Context, cfg Config, audioPath, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	tmp := outPath + ".tmp.wav"
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", audioPath,
		"-map", "0:a:0",
		"-vn", "-sn", "-dn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		tmp,
	}
	cmd := exec.CommandContext(ctx, cfg.FFmpegBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg pyannote audio conversion failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return os.Rename(tmp, outPath)
}

func resolvePyannoteDevice(ctx context.Context, pythonBin, requested string) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = defaultProgrammePyannoteDev
	}
	switch requested {
	case "cpu", "mps", "cuda":
		return requested, nil
	case "auto":
	default:
		return "", fmt.Errorf("--pyannote-device must be one of: auto, cpu, mps, cuda")
	}
	script := `
import torch
if torch.cuda.is_available():
    print("cuda")
elif hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
    print("mps")
else:
    print("cpu")
`
	cmd := exec.CommandContext(ctx, pythonBin, "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("pyannote device auto-detection failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	device := strings.TrimSpace(stdout.String())
	switch device {
	case "cpu", "mps", "cuda":
		return device, nil
	default:
		return "", fmt.Errorf("pyannote device auto-detection returned %q", device)
	}
}

func runPyannote(ctx context.Context, pythonBin, modelName, deviceName, audioPath, outPath string) ([]diarizationSegment, error) {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return nil, err
	}
	script := `
import json
import os
import sys

audio_path, model_name, device_name, out_path = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
os.environ.setdefault("PYTORCH_ENABLE_MPS_FALLBACK", "1")
token = os.environ.get("PYANNOTE_AUTH_TOKEN") or os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_TOKEN")
if not token:
    raise SystemExit("set PYANNOTE_AUTH_TOKEN, HF_TOKEN, or HUGGINGFACE_TOKEN for pyannote")
try:
    import torch
    from pyannote.audio import Pipeline
except Exception as exc:
    raise SystemExit(f"pyannote.audio is not installed in {sys.executable}: {exc}")
pipeline = Pipeline.from_pretrained(model_name, token=token)
if device_name == "mps":
    if not hasattr(torch.backends, "mps") or not torch.backends.mps.is_available():
        raise SystemExit("torch MPS is not available in this Python environment")
    pipeline.to(torch.device("mps"))
elif device_name == "cuda":
    if not torch.cuda.is_available():
        raise SystemExit("torch CUDA is not available in this Python environment")
    pipeline.to(torch.device("cuda"))
elif device_name == "cpu":
    pipeline.to(torch.device("cpu"))
else:
    raise SystemExit(f"unsupported pyannote device: {device_name}")
diarization = pipeline(audio_path)
if device_name == "mps" and hasattr(torch, "mps"):
    torch.mps.synchronize()
if hasattr(diarization, "exclusive_speaker_diarization"):
    diarization = diarization.exclusive_speaker_diarization
elif hasattr(diarization, "speaker_diarization"):
    diarization = diarization.speaker_diarization
segments = []
for turn, _, speaker in diarization.itertracks(yield_label=True):
    segments.append({"start": float(turn.start), "end": float(turn.end), "speaker": speaker})
with open(out_path, "w", encoding="utf-8") as f:
    json.dump({"segments": segments}, f, ensure_ascii=False, indent=2)
    f.write("\n")
`
	cmd := exec.CommandContext(ctx, pythonBin, "-c", script, audioPath, modelName, deviceName, outPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("pyannote diarization failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return readDiarization(outPath)
}

func readDiarization(path string) ([]diarizationSegment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Segments []diarizationSegment `json:"segments"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	return envelope.Segments, nil
}

func diarizedTranscript(out whisperOutput, segments []diarizationSegment) []diarizedParagraph {
	var paragraphs []diarizedParagraph
	var current diarizedParagraph
	flush := func() {
		current.Text = strings.TrimSpace(current.Text)
		if current.Text != "" {
			paragraphs = append(paragraphs, current)
		}
		current = diarizedParagraph{}
	}
	for _, word := range whisperWords(out) {
		text := strings.TrimSpace(word.Word)
		if text == "" {
			continue
		}
		speaker := speakerAt(segments, (word.Start+word.End)/2)
		if speaker == "" {
			speaker = "UNKNOWN"
		}
		if current.Speaker == "" {
			current = diarizedParagraph{Speaker: speaker, Start: word.Start, End: word.End}
		}
		if current.Speaker != speaker || word.Start-current.End > 2.0 {
			flush()
			current = diarizedParagraph{Speaker: speaker, Start: word.Start, End: word.End}
		}
		current.Text = appendTranscriptWord(current.Text, text)
		current.End = word.End
	}
	flush()
	return paragraphs
}

func whisperWords(out whisperOutput) []whisperWord {
	var words []whisperWord
	for _, segment := range out.Segments {
		words = append(words, segment.Words...)
	}
	return words
}

func speakerAt(segments []diarizationSegment, ts float64) string {
	var best diarizationSegment
	for _, segment := range segments {
		if ts >= segment.Start && ts <= segment.End {
			if best.Speaker == "" || segment.End-segment.Start < best.End-best.Start {
				best = segment
			}
		}
	}
	return best.Speaker
}

func mapProgrammeSpeakers(ctx context.Context, opts ProgrammeTranscribeOptions, window programmeWindow, paragraphs []diarizedParagraph) (programmeSpeakerMapMeta, error) {
	meta := programmeSpeakerMapMeta{Model: opts.CodexModel}
	prompt := speakerMapPrompt(window, paragraphs)
	raw, err := runCodexPrompt(ctx, opts, "tr1-speaker-map-*.txt", prompt)
	if err != nil {
		meta.Error = err.Error()
		return meta, err
	}
	meta.Raw = strings.TrimSpace(raw)
	mapping, err := parseSpeakerMapping(meta.Raw)
	if err != nil {
		meta.Error = err.Error()
		return meta, err
	}
	if len(mapping) == 0 {
		err := fmt.Errorf("codex speaker mapping response contained no speaker labels")
		meta.Error = err.Error()
		return meta, err
	}
	meta.Mapping = mapping
	return meta, nil
}

func runCodexPrompt(ctx context.Context, opts ProgrammeTranscribeOptions, tmpPattern, prompt string) (string, error) {
	tmp, err := os.CreateTemp("", tmpPattern)
	if err != nil {
		return "", err
	}
	outPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(outPath)
	cmd := exec.CommandContext(ctx, opts.CodexBin, "exec", "-m", opts.CodexModel, "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--output-last-message", outPath, "-")
	cmd.Stdin = strings.NewReader(prompt)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func speakerMapPrompt(window programmeWindow, paragraphs []diarizedParagraph) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You map anonymous diarization speaker labels to real speaker names for a radio programme.\n")
	fmt.Fprintf(&b, "Return only a compact JSON object mapping diarization labels to names, for example {\"SPEAKER_00\":\"Jan Nowak\"}. Use partial introductions such as first names or caller name plus city when that is all the programme gives. Ignore advertisements, station jingles, songs, sung lyrics, and music beds; labels used only for those should map to \"UNKNOWN\". Use \"UNKNOWN\" when there is not enough evidence.\n\n")
	fmt.Fprintf(&b, "Programme: %s\n", window.Entry.Programme)
	if window.Entry.Episode != "" {
		fmt.Fprintf(&b, "Episode: %s\n", window.Entry.Episode)
	}
	if len(window.Entry.Hosts) > 0 {
		fmt.Fprintf(&b, "Known hosts: %s\n", strings.Join(window.Entry.Hosts, ", "))
	}
	fmt.Fprintf(&b, "Scheduled local start: %s\n\n", window.StartLocal.Format(time.RFC3339))
	fmt.Fprintf(&b, "Beginning of diarized transcript:\n")
	appendSpeakerMapParagraphs(&b, paragraphs, 16000)
	fmt.Fprintf(&b, "\nRepresentative samples by diarization label:\n")
	for _, sample := range speakerMapSamples(paragraphs, 3, 600, 45) {
		fmt.Fprintf(&b, "%s: %s\n", sample.Speaker, sample.Text)
	}
	return b.String()
}

func appendSpeakerMapParagraphs(b *strings.Builder, paragraphs []diarizedParagraph, budget int) {
	used := 0
	for _, paragraph := range paragraphs {
		line := fmt.Sprintf("%s: %s\n", paragraph.Speaker, promptSnippet(paragraph.Text, 900))
		if used+len(line) > budget {
			fmt.Fprintf(b, "[transcript excerpt truncated]\n")
			return
		}
		b.WriteString(line)
		used += len(line)
	}
}

func speakerMapSamples(paragraphs []diarizedParagraph, perSpeaker, maxChars, maxSamples int) []diarizedParagraph {
	seen := make(map[string]int)
	var out []diarizedParagraph
	for _, paragraph := range paragraphs {
		if paragraph.Speaker == "" || paragraph.Speaker == "UNKNOWN" || strings.TrimSpace(paragraph.Text) == "" {
			continue
		}
		if isLikelyNonProgrammeSample(paragraph.Text) {
			continue
		}
		if seen[paragraph.Speaker] >= perSpeaker {
			continue
		}
		out = append(out, diarizedParagraph{
			Speaker: paragraph.Speaker,
			Text:    promptSnippet(paragraph.Text, maxChars),
		})
		seen[paragraph.Speaker]++
		if len(out) >= maxSamples {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Speaker == out[j].Speaker {
			return false
		}
		return out[i].Speaker < out[j].Speaker
	})
	return out
}

func isLikelyNonProgrammeSample(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	if isAdLikeText(text) {
		return true
	}
	return isLikelyMusicText(text)
}

func promptSnippet(s string, maxChars int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	if maxChars <= 3 {
		return string(runes[:maxChars])
	}
	return string(runes[:maxChars-3]) + "..."
}

func parseSpeakerMapping(raw string) (map[string]string, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("codex speaker mapping response did not contain a JSON object")
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(raw[start:end+1]), &mapping); err != nil {
		return nil, fmt.Errorf("parse codex speaker mapping JSON: %w", err)
	}
	return mapping, nil
}

func needsProgrammePostprocess(meta *programmePostprocessMeta, opts ProgrammeTranscribeOptions) bool {
	if meta == nil {
		return true
	}
	return meta.Version != programmePostprocessV1 || meta.Model != opts.CodexModel || meta.Error != ""
}

func postprocessProgrammeMarkdown(ctx context.Context, opts ProgrammeTranscribeOptions, window programmeWindow, paragraphs []diarizedParagraph, speakerMap *programmeSpeakerMapMeta) (programmePostprocessMeta, string, error) {
	meta := programmePostprocessMeta{
		Version: programmePostprocessV1,
		Model:   opts.CodexModel,
	}
	markdownParagraphs := programmeMarkdownParagraphs(paragraphs, speakerMap)
	if len(markdownParagraphs) == 0 {
		return meta, programmeMarkdownFromParagraphs(window, nil), nil
	}
	removeIDs := make(map[int]programmeRemovedBlock)
	rewrites := make(map[int]programmeRewrittenBlock)
	for _, batch := range programmePostprocessBatches(markdownParagraphs, 60, 30000) {
		raw, err := runCodexPrompt(ctx, opts, "tr1-ad-postprocess-*.txt", programmeAdPostprocessPrompt(window, batch))
		if err != nil {
			meta.Error = err.Error()
			return meta, programmeMarkdownFromParagraphs(window, markdownParagraphs), err
		}
		meta.RawResponses = append(meta.RawResponses, raw)
		decision, err := parseProgrammeAdDecision(raw)
		if err != nil {
			meta.Error = err.Error()
			return meta, programmeMarkdownFromParagraphs(window, markdownParagraphs), err
		}
		batchByID := make(map[int]programmeMarkdownParagraph, len(batch))
		for _, paragraph := range batch {
			batchByID[paragraph.ID] = paragraph
		}
		for _, removed := range decision.Remove {
			paragraph, ok := batchByID[removed.ID]
			if !ok {
				continue
			}
			removeIDs[removed.ID] = programmeRemovedBlock{
				ID:      removed.ID,
				Speaker: paragraph.Speaker,
				Start:   paragraph.Start,
				End:     paragraph.End,
				Text:    paragraph.Text,
				Reason:  strings.TrimSpace(removed.Reason),
			}
		}
		for _, rewrite := range decision.Rewrites {
			paragraph, ok := batchByID[rewrite.ID]
			if !ok {
				continue
			}
			text := strings.TrimSpace(rewrite.Text)
			if text == "" {
				removeIDs[rewrite.ID] = programmeRemovedBlock{
					ID:      rewrite.ID,
					Speaker: paragraph.Speaker,
					Start:   paragraph.Start,
					End:     paragraph.End,
					Text:    paragraph.Text,
					Reason:  strings.TrimSpace(rewrite.Reason),
				}
				continue
			}
			rewrites[rewrite.ID] = programmeRewrittenBlock{
				ID:       rewrite.ID,
				Speaker:  paragraph.Speaker,
				Start:    paragraph.Start,
				End:      paragraph.End,
				Original: paragraph.Text,
				Text:     text,
				Reason:   strings.TrimSpace(rewrite.Reason),
			}
		}
	}
	markMusicBridgeFragments(markdownParagraphs, removeIDs)
	cleaned := make([]programmeMarkdownParagraph, 0, len(markdownParagraphs))
	for _, paragraph := range markdownParagraphs {
		if removed, ok := removeIDs[paragraph.ID]; ok {
			meta.Removed = append(meta.Removed, removed)
			continue
		}
		if rewrite, ok := rewrites[paragraph.ID]; ok {
			paragraph.Text = rewrite.Text
			meta.Rewritten = append(meta.Rewritten, rewrite)
		}
		cleaned = append(cleaned, paragraph)
	}
	return meta, programmeMarkdownFromParagraphs(window, cleaned), nil
}

type programmeAdDecision struct {
	Remove   []programmeAdRemoveDecision  `json:"remove"`
	Rewrites []programmeAdRewriteDecision `json:"rewrites"`
}

type programmeAdRemoveDecision struct {
	ID     int    `json:"id"`
	Reason string `json:"reason"`
}

type programmeAdRewriteDecision struct {
	ID     int    `json:"id"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

func parseProgrammeAdDecision(raw string) (programmeAdDecision, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return programmeAdDecision{}, fmt.Errorf("codex ad cleanup response did not contain a JSON object")
	}
	var wire struct {
		Remove   json.RawMessage `json:"remove"`
		Rewrites json.RawMessage `json:"rewrites"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &wire); err != nil {
		return programmeAdDecision{}, fmt.Errorf("parse codex ad cleanup JSON: %w", err)
	}
	remove, err := parseProgrammeAdRemoveDecisions(wire.Remove)
	if err != nil {
		return programmeAdDecision{}, fmt.Errorf("parse codex ad cleanup remove JSON: %w", err)
	}
	rewrites, err := parseProgrammeAdRewriteDecisions(wire.Rewrites)
	if err != nil {
		return programmeAdDecision{}, fmt.Errorf("parse codex ad cleanup rewrites JSON: %w", err)
	}
	decision := programmeAdDecision{Remove: remove, Rewrites: rewrites}
	return decision, nil
}

func parseProgrammeAdRemoveDecisions(raw json.RawMessage) ([]programmeAdRemoveDecision, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		out := make([]programmeAdRemoveDecision, 0, len(items))
		for _, item := range items {
			decision, ok, err := parseProgrammeAdRemoveDecision(item)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, decision)
			}
		}
		return out, nil
	}
	decision, ok, err := parseProgrammeAdRemoveDecision(raw)
	if err != nil || !ok {
		return nil, err
	}
	return []programmeAdRemoveDecision{decision}, nil
}

func parseProgrammeAdRemoveDecision(raw json.RawMessage) (programmeAdRemoveDecision, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return programmeAdRemoveDecision{}, false, nil
	}
	if raw[0] == '{' {
		var decision programmeAdRemoveDecision
		if err := json.Unmarshal(raw, &decision); err != nil {
			return programmeAdRemoveDecision{}, false, err
		}
		if decision.ID == 0 {
			return programmeAdRemoveDecision{}, false, nil
		}
		if strings.TrimSpace(decision.Reason) == "" {
			decision.Reason = "non-programme audio"
		}
		return decision, true, nil
	}
	var id int
	if err := json.Unmarshal(raw, &id); err == nil {
		if id == 0 {
			return programmeAdRemoveDecision{}, false, nil
		}
		return programmeAdRemoveDecision{ID: id, Reason: "non-programme audio"}, true, nil
	}
	var idString string
	if err := json.Unmarshal(raw, &idString); err != nil {
		return programmeAdRemoveDecision{}, false, err
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(idString))
	if err != nil || parsed == 0 {
		return programmeAdRemoveDecision{}, false, err
	}
	return programmeAdRemoveDecision{ID: parsed, Reason: "non-programme audio"}, true, nil
}

func parseProgrammeAdRewriteDecisions(raw json.RawMessage) ([]programmeAdRewriteDecision, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '[' {
		var rewrites []programmeAdRewriteDecision
		if err := json.Unmarshal(raw, &rewrites); err != nil {
			return nil, err
		}
		return rewrites, nil
	}
	var rewrite programmeAdRewriteDecision
	if err := json.Unmarshal(raw, &rewrite); err != nil {
		return nil, err
	}
	if rewrite.ID == 0 {
		return nil, nil
	}
	return []programmeAdRewriteDecision{rewrite}, nil
}

func markMusicBridgeFragments(paragraphs []programmeMarkdownParagraph, removeIDs map[int]programmeRemovedBlock) {
	for _, paragraph := range paragraphs {
		if _, removed := removeIDs[paragraph.ID]; removed {
			continue
		}
		if !isShortMusicBridge(paragraph, removeIDs) {
			continue
		}
		removeIDs[paragraph.ID] = programmeRemovedBlock{
			ID:      paragraph.ID,
			Speaker: paragraph.Speaker,
			Start:   paragraph.Start,
			End:     paragraph.End,
			Text:    paragraph.Text,
			Reason:  "music fragment",
		}
	}
}

func isShortMusicBridge(paragraph programmeMarkdownParagraph, removeIDs map[int]programmeRemovedBlock) bool {
	if len(strings.Fields(paragraph.Text)) > 4 {
		return false
	}
	prev, prevRemoved := removeIDs[paragraph.ID-1]
	next, nextRemoved := removeIDs[paragraph.ID+1]
	return (prevRemoved && removedReasonLooksLikeMusic(prev.Reason)) || (nextRemoved && removedReasonLooksLikeMusic(next.Reason))
}

func removedReasonLooksLikeMusic(reason string) bool {
	normalized := strings.ToLower(removePolishDiacritics(reason))
	for _, cue := range []string{"music", "song", "lyric", "jingle", "sung"} {
		if strings.Contains(normalized, cue) {
			return true
		}
	}
	return false
}

func programmeAdPostprocessPrompt(window programmeWindow, paragraphs []programmeMarkdownParagraph) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You clean a radio programme transcript after transcription and diarization.\n")
	fmt.Fprintf(&b, "Identify advertising, sponsorship, paid announcements, station autopromotion, cross-promotion, product/service offers, legal ad disclaimers, jingles, ad blocks, music beds, songs, sung lyrics, music-video/subtitle artifacts, and music-only transcript fragments in any language. Cues may include words like reklama, autopromocja, sponsor, advertisement, commercial break, promo, brought to you by, visit/buy/check our offer, brand slogans, prices, product claims, medical/supplement disclaimers, URLs, phone-number sales calls, lyrical lines, repeated choruses, singer-like English/Polish fragments, and paragraphs following a presenter introducing a track.\n")
	fmt.Fprintf(&b, "Remove sung lyrics and music/jingle fragments even when Whisper transcribed them as normal words or pyannote assigned them to a speaker. Keep spoken presenter introductions to songs, artist facts, interviews about music, editorial discussion about advertising, politics, economy, brands, medicine, media, or commercials when it is programme content rather than a promotion. If unsure, keep it.\n")
	fmt.Fprintf(&b, "Return only compact JSON with this shape: {\"remove\":[{\"id\":12,\"reason\":\"ad block\"}],\"rewrites\":[{\"id\":18,\"text\":\"speaker words after cutting embedded promo\",\"reason\":\"removed embedded promo\"}]}. Use rewrites only when a paragraph mixes programme content with ad/promotional content; preserve wording otherwise. Return empty arrays when nothing should be removed.\n\n")
	fmt.Fprintf(&b, "Programme: %s\n", window.Entry.Programme)
	if window.Entry.Episode != "" {
		fmt.Fprintf(&b, "Episode: %s\n", window.Entry.Episode)
	}
	if len(window.Entry.Hosts) > 0 {
		fmt.Fprintf(&b, "Known hosts: %s\n", strings.Join(window.Entry.Hosts, ", "))
	}
	fmt.Fprintf(&b, "Scheduled local start: %s\n\n", window.StartLocal.Format(time.RFC3339))
	fmt.Fprintf(&b, "Transcript paragraphs:\n")
	for _, paragraph := range paragraphs {
		fmt.Fprintf(&b, "[%d] %.1f-%.1f %s: %s\n", paragraph.ID, paragraph.Start, paragraph.End, paragraph.Speaker, promptSnippet(paragraph.Text, 1200))
	}
	return b.String()
}

func programmePostprocessBatches(paragraphs []programmeMarkdownParagraph, maxParagraphs, maxChars int) [][]programmeMarkdownParagraph {
	var batches [][]programmeMarkdownParagraph
	var batch []programmeMarkdownParagraph
	used := 0
	flush := func() {
		if len(batch) > 0 {
			batches = append(batches, batch)
			batch = nil
			used = 0
		}
	}
	for _, paragraph := range paragraphs {
		size := len(paragraph.Speaker) + len(paragraph.Text) + 64
		if len(batch) > 0 && (len(batch) >= maxParagraphs || used+size > maxChars) {
			flush()
		}
		batch = append(batch, paragraph)
		used += size
	}
	flush()
	return batches
}

func programmeMarkdownParagraphs(paragraphs []diarizedParagraph, speakerMap *programmeSpeakerMapMeta) []programmeMarkdownParagraph {
	out := make([]programmeMarkdownParagraph, 0, len(paragraphs))
	for i, paragraph := range paragraphs {
		text := strings.TrimSpace(paragraph.Text)
		if text == "" {
			continue
		}
		out = append(out, programmeMarkdownParagraph{
			ID:      i + 1,
			Speaker: mappedSpeakerName(paragraph.Speaker, speakerMap),
			Text:    text,
			Start:   paragraph.Start,
			End:     paragraph.End,
		})
	}
	return out
}

func mappedSpeakerName(speaker string, speakerMap *programmeSpeakerMapMeta) string {
	if speakerMap != nil && speakerMap.Mapping != nil {
		if mapped := strings.TrimSpace(speakerMap.Mapping[speaker]); mapped != "" {
			return mapped
		}
	}
	return speaker
}

func programmeMarkdownFromParagraphs(window programmeWindow, paragraphs []programmeMarkdownParagraph) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", programmeMarkdownTitle(window))
	for _, paragraph := range paragraphs {
		text := strings.TrimSpace(paragraph.Text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n\n", paragraph.Speaker, text)
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func programmeMarkdown(window programmeWindow, out whisperOutput, paragraphs []diarizedParagraph, speakerMap *programmeSpeakerMapMeta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", programmeMarkdownTitle(window))
	if len(paragraphs) > 0 {
		for _, paragraph := range paragraphs {
			name := mappedSpeakerName(paragraph.Speaker, speakerMap)
			text := adFilteredText(paragraph.Text)
			if text == "" {
				continue
			}
			fmt.Fprintf(&b, "%s: %s\n\n", name, text)
		}
		return strings.TrimSpace(b.String()) + "\n"
	}
	text := adFilteredText(out.Text)
	if text != "" {
		fmt.Fprintf(&b, "%s\n", text)
	}
	return b.String()
}

func programmeMarkdownTitle(window programmeWindow) string {
	title := strings.TrimSpace(window.Entry.Programme)
	if window.Entry.Episode != "" {
		title += ": " + strings.TrimSpace(window.Entry.Episode)
	}
	return title
}

func adFilteredText(text string) string {
	sentences := splitSentences(text)
	var kept []string
	for _, sentence := range sentences {
		if isAdLikeSentence(sentence) {
			continue
		}
		kept = append(kept, sentence)
	}
	if len(kept) == 0 {
		return strings.TrimSpace(text)
	}
	return strings.Join(kept, " ")
}

func isAdLikeText(text string) bool {
	for _, sentence := range splitSentences(text) {
		if isAdLikeSentence(sentence) {
			return true
		}
	}
	return false
}

func splitSentences(text string) []string {
	fields := strings.Fields(text)
	var out []string
	var current []string
	for _, field := range fields {
		current = append(current, field)
		if strings.HasSuffix(field, ".") || strings.HasSuffix(field, "?") || strings.HasSuffix(field, "!") {
			out = append(out, strings.Join(current, " "))
			current = nil
		}
	}
	if len(current) > 0 {
		out = append(out, strings.Join(current, " "))
	}
	return out
}

func isAdLikeSentence(sentence string) bool {
	normalized := strings.ToLower(removePolishDiacritics(sentence))
	cues := []string{
		"reklama",
		"material sponsorowany",
		"partnerem audycji",
		"sponsorem audycji",
		"podmiot prowadzacy reklame",
		"suplement diety",
		"wyrob medyczny",
		"uzywaj go zgodnie",
		"stosuj go zgodnie",
		"skonsultuj sie z lekarzem",
		"skonsultuj sie z farmaceuta",
		"tabletki powlekane",
		"bezplatne probki",
		"wiecej informacji",
		"sport.pl",
		"mercedes",
		"baic",
		"zlotych miesiecznie",
		"ubezpieczeniem za zlotowke",
		"zapraszamy do naszych salonow",
		"sprawdz tez oferte",
		"kup teraz",
		"promocja",
		"oferta wazna",
		"sprawdz na",
	}
	for _, cue := range cues {
		if strings.Contains(normalized, cue) {
			return true
		}
	}
	return false
}

func isLikelyMusicText(text string) bool {
	normalized := strings.ToLower(removePolishDiacritics(text))
	cues := []string{
		"no one knows what it's like",
		"behind blue eyes",
		"let's dance",
		"napisy stworzone przez spolecznosc amara",
		"subtitles by",
		"lyrics",
	}
	for _, cue := range cues {
		if strings.Contains(normalized, cue) {
			return true
		}
	}
	words := strings.Fields(normalized)
	if len(words) < 4 {
		return false
	}
	englishHits := 0
	for _, word := range words {
		switch strings.Trim(word, ".,!?;:\"'()[]{}") {
		case "what", "it's", "like", "to", "be", "the", "and", "you", "i", "me", "my", "your", "love", "know", "knows", "feel", "feeling", "heart", "eyes", "blue", "dance":
			englishHits++
		}
	}
	return englishHits >= 5 && englishHits*2 >= len(words)
}

func writeProgrammeTranscript(path string, transcript programmeTranscript) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(transcript, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeProgrammeMarkdown(path, markdown string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(markdown), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type programmeSummaryRow struct {
	Status string
	Window programmeWindow
	Path   string
	Note   string
}

func writeProgrammeTranscriptSummary(w io.Writer, rows []programmeSummaryRow) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "STATUS\tSTART_LOCAL\tDURATION\tPRIORITY\tPROGRAMME\tPATH\tNOTE"); err != nil {
		return err
	}
	for _, row := range rows {
		note := row.Note
		if note == "" && row.Window.CoverageStatus == "partial" {
			note = "partial cached coverage"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\t%s\t%s\n",
			row.Status,
			row.Window.StartLocal.Format("2006-01-02 15:04 MST"),
			row.Window.AudioEndUTC.Sub(row.Window.AudioStartUTC).Round(time.Second),
			row.Window.Priority,
			windowDisplayName(row.Window),
			row.Path,
			note,
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func programmeAudioPath(cacheRoot string, window programmeWindow, loc *time.Location) string {
	local := window.StartLocal.In(loc)
	return filepath.Join(
		cacheRoot,
		"programmes",
		programmeAudioCacheV1,
		window.Station,
		local.Format("2006"),
		local.Format("01"),
		local.Format("02"),
		window.ID+defaultRecordExt,
	)
}

func programmeTranscriptPath(cacheRoot string, window programmeWindow, backend string, cfg Config, loc *time.Location) string {
	local := window.StartLocal.In(loc)
	return filepath.Join(
		cacheRoot,
		"programmes",
		"transcripts",
		programmeTranscriptCacheV1,
		window.Station,
		local.Format("2006"),
		local.Format("01"),
		local.Format("02"),
		window.ID,
		stationSlug(backend),
		stationSlug(cfg.Model),
		stationSlug(cfg.Language)+".json",
	)
}

func programmeDiarizationPath(cacheRoot string, window programmeWindow, loc *time.Location, modelName, device string) string {
	local := window.StartLocal.In(loc)
	return filepath.Join(
		cacheRoot,
		"programmes",
		"diarization",
		programmeDiarizationCacheV1,
		window.Station,
		stationSlug(modelName),
		stationSlug(device),
		local.Format("2006"),
		local.Format("01"),
		local.Format("02"),
		window.ID+".json",
	)
}

func programmeDiarizationAudioPath(cacheRoot string, window programmeWindow, loc *time.Location) string {
	local := window.StartLocal.In(loc)
	return filepath.Join(
		cacheRoot,
		"programmes",
		"diarization-audio",
		programmeDiarizationAudioV1,
		window.Station,
		local.Format("2006"),
		local.Format("01"),
		local.Format("02"),
		window.ID+".wav",
	)
}

func programmeTranscriptMetaFromWindow(window programmeWindow) programmeTranscriptMeta {
	return programmeTranscriptMeta{
		Station:      window.Station,
		Programme:    window.Entry.Programme,
		Episode:      window.Entry.Episode,
		Hosts:        append([]string(nil), window.Entry.Hosts...),
		PodcastID:    window.Entry.PodcastID,
		ProgrammeURL: window.Entry.ProgrammeURL,
		EpisodeURL:   window.Entry.EpisodeURL,
		StartLocal:   window.StartLocal.Format(time.RFC3339),
		EndLocal:     window.EndLocal.Format(time.RFC3339),
		StartUTC:     window.StartUTC.Format(time.RFC3339),
		EndUTC:       window.EndUTC.Format(time.RFC3339),
		Priority:     window.Priority,
	}
}

func programmeWindowID(window programmeWindow) string {
	parts := []string{window.StartLocal.Format("1504")}
	if window.Entry.PodcastID != "" {
		parts = append(parts, window.Entry.PodcastID)
	}
	parts = append(parts, window.Entry.Programme)
	if window.Entry.Episode != "" {
		parts = append(parts, window.Entry.Episode)
	}
	return stationSlug(strings.Join(parts, "-"))
}

func windowDisplayName(window programmeWindow) string {
	name := strings.TrimSpace(window.Entry.Programme)
	if window.Entry.Episode != "" {
		name += ": " + strings.TrimSpace(window.Entry.Episode)
	}
	return name
}

func parseClock(raw string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

var durationNumberRE = regexp.MustCompile(`(\d+)`)

func parseProgrammeDuration(raw string) time.Duration {
	normalized := strings.ToLower(removePolishDiacritics(raw))
	matches := durationNumberRE.FindAllString(normalized, -1)
	if len(matches) == 0 {
		return 0
	}
	var minutes int
	if strings.Contains(normalized, "godz") || strings.Contains(normalized, "hour") {
		hours, _ := strconv.Atoi(matches[0])
		minutes += hours * 60
		if len(matches) > 1 {
			extra, _ := strconv.Atoi(matches[1])
			minutes += extra
		}
	} else {
		minutes, _ = strconv.Atoi(matches[0])
	}
	return time.Duration(minutes) * time.Minute
}

func isPriorityProgramme(entry programmes.Entry) bool {
	text := strings.ToLower(removePolishDiacritics(strings.Join([]string{
		entry.Programme,
		entry.Episode,
		strings.Join(entry.Hosts, " "),
	}, " ")))
	keywords := []string{
		"poranek",
		"pierwszy program",
		"popoludnie",
		"tok360",
		"podsumowanie dnia",
		"mikrofon",
		"off czarek",
		"polity",
		"sejm",
		"rzad",
		"prezydent",
		"ekonomia",
		"gospodarka",
		"informac",
		"wiadomos",
	}
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func removePolishDiacritics(s string) string {
	replacer := strings.NewReplacer(
		"ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n", "ó", "o", "ś", "s", "ź", "z", "ż", "z",
		"Ą", "A", "Ć", "C", "Ę", "E", "Ł", "L", "Ń", "N", "Ó", "O", "Ś", "S", "Ź", "Z", "Ż", "Z",
	)
	return replacer.Replace(s)
}

func midnightInLocation(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

func weekdayIndex(day time.Weekday) int {
	if day == time.Sunday {
		return 6
	}
	return int(day - time.Monday)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func appendTranscriptWord(text, word string) string {
	if text == "" {
		return word
	}
	if needsSpace(word) {
		return text + " " + word
	}
	return text + word
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

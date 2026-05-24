package tr1

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"tr1/internal/programmes"
)

func TestLookupStationAliases(t *testing.T) {
	tests := map[string]string{
		"":                              "TokFM",
		"tok":                           "TokFM",
		"jedynka":                       "Polskie Radio Jedynka",
		"pr1":                           "Polskie Radio Jedynka",
		"Program Drugi Polskiego Radia": "Program Drugi Polskiego Radia",
		"dwojka":                        "Program Drugi Polskiego Radia",
		"dwójka":                        "Program Drugi Polskiego Radia",
		"trojka":                        "Trójka",
		"Trójka":                        "Trójka",
		"rmf":                           "RMF FM",
		"rmf-fm":                        "RMF FM",
		"zet":                           "Radio ZET",
		"Radio ZET":                     "Radio ZET",
		"bbc":                           "BBC World Service",
		"bbc-world-service":             "BBC World Service",
		"english":                       "BBC World Service",
	}

	for query, want := range tests {
		got, err := lookupStation(query)
		if err != nil {
			t.Fatalf("lookupStation(%q) returned error: %v", query, err)
		}
		if got.Name != want {
			t.Fatalf("lookupStation(%q) = %q, want %q", query, got.Name, want)
		}
	}
}

func TestApplySelectedStationDefaultsUsesStationLanguage(t *testing.T) {
	cfg := Config{Station: "bbc", Language: "Polish"}

	name, url, err := applySelectedStationDefaults(&cfg)
	if err != nil {
		t.Fatalf("applySelectedStationDefaults returned error: %v", err)
	}
	if name != "BBC World Service" {
		t.Fatalf("station name = %q, want BBC World Service", name)
	}
	if url != "https://stream.live.vc.bbcmedia.co.uk/bbc_world_service" {
		t.Fatalf("station URL = %q, want BBC World Service URL", url)
	}
	if cfg.Language != "English" {
		t.Fatalf("language = %q, want English", cfg.Language)
	}
}

func TestApplySelectedStationDefaultsKeepsLanguageOverride(t *testing.T) {
	cfg := Config{Station: "bbc", Language: "Spanish", LanguageSet: true}

	if _, _, err := applySelectedStationDefaults(&cfg); err != nil {
		t.Fatalf("applySelectedStationDefaults returned error: %v", err)
	}
	if cfg.Language != "Spanish" {
		t.Fatalf("language = %q, want explicit override Spanish", cfg.Language)
	}
}

func TestApplySelectedStationDefaultsSkipsCustomStream(t *testing.T) {
	cfg := Config{
		Station:   "bbc",
		StreamURL: "https://example.com/custom.mp3",
		Language:  "Polish",
	}

	name, url, err := applySelectedStationDefaults(&cfg)
	if err != nil {
		t.Fatalf("applySelectedStationDefaults returned error: %v", err)
	}
	if name != "custom stream" {
		t.Fatalf("station name = %q, want custom stream", name)
	}
	if url != "https://example.com/custom.mp3" {
		t.Fatalf("station URL = %q, want custom URL", url)
	}
	if cfg.Language != "Polish" {
		t.Fatalf("language = %q, want unchanged Polish", cfg.Language)
	}
}

func TestStreamSelectionCustomURL(t *testing.T) {
	gotName, gotURL, err := streamSelection(Config{
		Station:   "rmf",
		StreamURL: "https://example.com/custom.mp3",
	})
	if err != nil {
		t.Fatalf("streamSelection returned error: %v", err)
	}
	if gotName != "custom stream" {
		t.Fatalf("streamSelection name = %q, want custom stream", gotName)
	}
	if gotURL != "https://example.com/custom.mp3" {
		t.Fatalf("streamSelection URL = %q, want custom URL", gotURL)
	}
}

func TestLookupStationUnknown(t *testing.T) {
	if _, err := lookupStation("missing"); err == nil {
		t.Fatal("lookupStation returned nil error for unknown station")
	}
}

func TestLookupFixtureAliases(t *testing.T) {
	tests := map[string]string{
		"":                  "biebrza-broadcast",
		"news-preview":      "news-preview",
		"biebrza-broadcast": "biebrza-broadcast",
	}

	for query, want := range tests {
		got, err := lookupFixture(query)
		if err != nil {
			t.Fatalf("lookupFixture(%q) returned error: %v", query, err)
		}
		if got.Name != want {
			t.Fatalf("lookupFixture(%q) = %q, want %q", query, got.Name, want)
		}
	}
}

func TestTerminalWordWriterWrapsBeforeSplittingWord(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 17)

	for _, word := range []string{"znów", "furmanko", "tam"} {
		if err := writer.writeWord(word, true); err != nil {
			t.Fatalf("writeWord(%q) returned error: %v", word, err)
		}
	}

	want := "znów furmanko \ntam "
	if got := out.String(); got != want {
		t.Fatalf("wrapped output = %q, want %q", got, want)
	}
}

func TestTerminalWordWriterDoesNotWrapWhenWordFitsExactly(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 14)

	for _, word := range []string{"znów", "furmanko"} {
		if err := writer.writeWord(word, true); err != nil {
			t.Fatalf("writeWord(%q) returned error: %v", word, err)
		}
	}

	want := "znów furmanko "
	if got := out.String(); got != want {
		t.Fatalf("wrapped output = %q, want %q", got, want)
	}
}

func TestTerminalWordWriterLeavesPipedOutputUnwrapped(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 0)

	for _, word := range []string{"znów", "furmanko", "tam"} {
		if err := writer.writeWord(word, true); err != nil {
			t.Fatalf("writeWord(%q) returned error: %v", word, err)
		}
	}

	want := "znów furmanko tam "
	if got := out.String(); got != want {
		t.Fatalf("piped output = %q, want %q", got, want)
	}
}

func TestTerminalWordWriterSpinnerClearsBeforeWord(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 20)
	writer.spinnerEnabled = true

	if err := writer.tickSpinner(); err != nil {
		t.Fatalf("tickSpinner returned error: %v", err)
	}
	if err := writer.writeWord("hello", true); err != nil {
		t.Fatalf("writeWord returned error: %v", err)
	}

	want := " ⣾\b\b  \b\bhello "
	if got := out.String(); got != want {
		t.Fatalf("spinner output = %q, want %q", got, want)
	}
	if writer.col != 6 {
		t.Fatalf("writer col = %d, want 6", writer.col)
	}
}

func TestTerminalWordWriterSpinnerTicksFrames(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 20)
	writer.spinnerEnabled = true

	if err := writer.tickSpinner(); err != nil {
		t.Fatalf("first tickSpinner returned error: %v", err)
	}
	if err := writer.tickSpinner(); err != nil {
		t.Fatalf("second tickSpinner returned error: %v", err)
	}

	want := " ⣾\b\b  \b\b ⣽"
	if got := out.String(); got != want {
		t.Fatalf("spinner output = %q, want %q", got, want)
	}
	if writer.col != 0 {
		t.Fatalf("writer col = %d, want 0", writer.col)
	}
}

func TestTerminalWordWriterSpinnerSkipsWhenDisabled(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 20)

	if err := writer.tickSpinner(); err != nil {
		t.Fatalf("tickSpinner returned error: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("spinner output = %q, want empty output", got)
	}
}

func TestTerminalWordWriterSpinnerDoesNotWrapLine(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 6)
	writer.spinnerEnabled = true

	if err := writer.writeWord("hello", true); err != nil {
		t.Fatalf("writeWord returned error: %v", err)
	}
	if err := writer.tickSpinner(); err != nil {
		t.Fatalf("tickSpinner returned error: %v", err)
	}

	want := "hello "
	if got := out.String(); got != want {
		t.Fatalf("spinner output = %q, want %q", got, want)
	}
}

func TestTerminalWordWriterCloseClearsSpinner(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 20)
	writer.spinnerEnabled = true

	if err := writer.tickSpinner(); err != nil {
		t.Fatalf("tickSpinner returned error: %v", err)
	}
	writer.close()

	want := " ⣾" + eraseCells(displayWidth(" ⣾"))
	if got := out.String(); got != want {
		t.Fatalf("close output = %q, want %q", got, want)
	}
}

func TestTerminalWordWriterTuningTicksFrames(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 30)
	writer.spinnerEnabled = true

	if err := writer.tickTuning(); err != nil {
		t.Fatalf("first tickTuning returned error: %v", err)
	}
	if err := writer.tickTuning(); err != nil {
		t.Fatalf("second tickTuning returned error: %v", err)
	}
	if err := writer.tickTuning(); err != nil {
		t.Fatalf("third tickTuning returned error: %v", err)
	}

	want := "📻 Tuning in." + eraseCells(displayWidth("📻 Tuning in.")) +
		"📻 Tuning in.." + eraseCells(displayWidth("📻 Tuning in..")) +
		"📻 Tuning in..."
	if got := out.String(); got != want {
		t.Fatalf("tuning output = %q, want %q", got, want)
	}
	if writer.col != 0 {
		t.Fatalf("writer col = %d, want 0", writer.col)
	}
}

func TestTerminalWordWriterTuningClearsBeforeFirstWord(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 30)
	writer.spinnerEnabled = true

	if err := writer.tickTuning(); err != nil {
		t.Fatalf("tickTuning returned error: %v", err)
	}
	if err := writer.writeWord("hello", true); err != nil {
		t.Fatalf("writeWord returned error: %v", err)
	}

	want := "📻 Tuning in." + eraseCells(displayWidth("📻 Tuning in.")) + "hello "
	if got := out.String(); got != want {
		t.Fatalf("tuning output = %q, want %q", got, want)
	}
	if writer.col != 6 {
		t.Fatalf("writer col = %d, want 6", writer.col)
	}
}

func TestTerminalWordWriterCloseClearsTuning(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 30)
	writer.spinnerEnabled = true

	if err := writer.tickTuning(); err != nil {
		t.Fatalf("tickTuning returned error: %v", err)
	}
	writer.close()

	want := "📻 Tuning in." + eraseCells(displayWidth("📻 Tuning in."))
	if got := out.String(); got != want {
		t.Fatalf("close output = %q, want %q", got, want)
	}
}

func TestDisplayWidthCountsEmojiAsWide(t *testing.T) {
	if got := displayWidth("📻"); got != 2 {
		t.Fatalf("displayWidth(radio emoji) = %d, want 2", got)
	}
}

func TestTerminalWordWriterSpinnerAppearsAfterPrintedWords(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 20)
	writer.spinnerEnabled = true
	printedUntil := 0.0

	resp := whisperResponse{
		Seq:      1,
		Offset:   0,
		Duration: 10,
		Words: []whisperWord{
			{Word: " hello", Start: 0, End: 1},
			{Word: " world", Start: 1, End: 2},
		},
	}

	if err := printStableWords(t.Context(), Config{}, writer, resp, 10, &printedUntil); err != nil {
		t.Fatalf("printStableWords returned error: %v", err)
	}
	if err := writer.tickSpinner(); err != nil {
		t.Fatalf("tickSpinner returned error: %v", err)
	}

	want := "hello world  ⣾"
	if got := out.String(); got != want {
		t.Fatalf("spinner output = %q, want %q", got, want)
	}
}

func eraseCells(width int) string {
	return strings.Repeat("\b", width) + strings.Repeat(" ", width) + strings.Repeat("\b", width)
}

func TestPrintStableWordsUsesTerminalWriter(t *testing.T) {
	var out bytes.Buffer
	writer := newTestTerminalWordWriter(&out, 17)
	printedUntil := 0.0

	resp := whisperResponse{
		Seq:      1,
		Offset:   0,
		Duration: 10,
		Words: []whisperWord{
			{Word: " znów", Start: 0, End: 1},
			{Word: " furmanko", Start: 1, End: 2},
			{Word: " tam", Start: 2, End: 3},
		},
	}

	if err := printStableWords(t.Context(), Config{}, writer, resp, 10, &printedUntil); err != nil {
		t.Fatalf("printStableWords returned error: %v", err)
	}

	want := "znów furmanko \ntam "
	if got := out.String(); got != want {
		t.Fatalf("printed output = %q, want %q", got, want)
	}
}

func TestAudioMonitorCommandConsumesPCMFromStdin(t *testing.T) {
	cmd := audioMonitorCommand(t.Context(), Config{FFplayBin: "ffplay"})

	got := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"ffplay",
		"-f s16le",
		"-sample_rate 16000",
		"-ch_layout mono",
		"-i pipe:0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("audio monitor args missing %q: %s", want, got)
		}
	}
}

func TestTerminalResizeListenerRefreshesWidthOnSIGWINCH(t *testing.T) {
	var width atomic.Int32
	width.Store(12)
	columns := &terminalColumns{
		enabled: true,
		provider: func(uintptr) (int, bool) {
			return int(width.Load()), true
		},
	}
	columns.refresh()

	signals := make(chan os.Signal, 1)
	stop := startTerminalResizeListener(t.Context(), columns, signals)
	defer stop()

	width.Store(24)
	signals <- syscall.SIGWINCH

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := columns.current(); got == 24 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("terminal width = %d, want 24 after SIGWINCH", columns.current())
}

func TestRecordingCacheRootUsesXDGCacheHome(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	got, err := recordingCacheRoot(Config{})
	if err != nil {
		t.Fatalf("recordingCacheRoot returned error: %v", err)
	}
	want := filepath.Join(cacheHome, "tr1")
	if got != want {
		t.Fatalf("recording cache root = %q, want %q", got, want)
	}
}

func TestRecordingCacheRootUsesExplicitCacheDirAsRoot(t *testing.T) {
	cacheRoot := t.TempDir()

	got, err := recordingCacheRoot(Config{CacheDir: cacheRoot})
	if err != nil {
		t.Fatalf("recordingCacheRoot returned error: %v", err)
	}
	if got != cacheRoot {
		t.Fatalf("recording cache root = %q, want explicit root %q", got, cacheRoot)
	}
}

func TestRecordingSegmentPatternUsesUTCStrftimePath(t *testing.T) {
	cacheRoot := t.TempDir()

	got := recordingSegmentPattern(cacheRoot, "tokfm")
	want := filepath.Join(cacheRoot, "recordings", "tokfm", "%Y", "%m", "%d", "tokfm_%Y%m%dT%H%M%SZ.mka")
	if got != want {
		t.Fatalf("recording segment pattern = %q, want %q", got, want)
	}
}

func TestRecordListCommandListsStationRecordings(t *testing.T) {
	cacheRoot := t.TempDir()
	tokPath := filepath.Join(cacheRoot, "recordings", "tokfm", "2026", "05", "18", "tokfm_20260518T120000Z.mka")
	rmfPath := filepath.Join(cacheRoot, "recordings", "rmf", "2026", "05", "18", "rmf_20260518T130000Z.mka")
	for _, path := range []string{tokPath, rmfPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}

	var out bytes.Buffer
	if err := runRecordList(&out, Config{CacheDir: cacheRoot, Station: "tok"}); err != nil {
		t.Fatalf("runRecordList returned error: %v", err)
	}

	got := out.String()
	for _, want := range []string{"START_UTC", "STATION", "BYTES", "PATH", "2026-05-18T12:00:00Z", "tokfm", tokPath} {
		if !strings.Contains(got, want) {
			t.Fatalf("record list output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, rmfPath) {
		t.Fatalf("record list output included another Station:\n%s", got)
	}
}

func TestRecordListCommandWithoutStationListsAllRecordings(t *testing.T) {
	cacheRoot := t.TempDir()
	for _, path := range []string{
		filepath.Join(cacheRoot, "recordings", "tokfm", "2026", "05", "18", "tokfm_20260518T120000Z.mka"),
		filepath.Join(cacheRoot, "recordings", "rmf", "2026", "05", "18", "rmf_20260518T130000Z.mka"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}

	var out bytes.Buffer
	if err := runRecordList(&out, Config{CacheDir: cacheRoot}); err != nil {
		t.Fatalf("runRecordList returned error: %v", err)
	}

	got := out.String()
	for _, want := range []string{"tokfm", "rmf", "2026-05-18T12:00:00Z", "2026-05-18T13:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Fatalf("record list output missing %q:\n%s", want, got)
		}
	}
}

func TestRecordListSkipsEmptyInterruptedSegments(t *testing.T) {
	cacheRoot := t.TempDir()
	fullPath := filepath.Join(cacheRoot, "recordings", "bbc", "2026", "05", "18", "bbc_20260518T120000Z.mka")
	emptyPath := filepath.Join(cacheRoot, "recordings", "bbc", "2026", "05", "18", "bbc_20260518T120500Z.mka")
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("audio"), 0o644); err != nil {
		t.Fatalf("WriteFile full recording returned error: %v", err)
	}
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile empty recording returned error: %v", err)
	}

	recordings, err := listRecordings(cacheRoot, "bbc")
	if err != nil {
		t.Fatalf("listRecordings returned error: %v", err)
	}
	if len(recordings) != 1 {
		t.Fatalf("recording count = %d, want 1: %#v", len(recordings), recordings)
	}
	if recordings[0].Path != fullPath {
		t.Fatalf("recording path = %q, want %q", recordings[0].Path, fullPath)
	}
}

func TestRecordingTranscriptPathIsVersionedByRecordingAndModel(t *testing.T) {
	cacheRoot := t.TempDir()
	rec := recording{
		Station:  "bbc",
		StartUTC: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		Path:     filepath.Join(cacheRoot, "recordings", "bbc", "2026", "05", "18", "bbc_20260518T120000Z.mka"),
	}

	got := recordingTranscriptPath(cacheRoot, rec, backendCPU, Config{Model: "tiny.en", Language: "English"})
	want := filepath.Join(cacheRoot, "transcripts", transcriptCacheV1, "bbc", "2026", "05", "18", "bbc-20260518t120000z", "cpu", "tiny-en", "english.json")
	if got != want {
		t.Fatalf("recording transcript path = %q, want %q", got, want)
	}
}

func TestResolveRecordingTargetPicksLatestStationRecording(t *testing.T) {
	cacheRoot := t.TempDir()
	oldPath := filepath.Join(cacheRoot, "recordings", "bbc", "2026", "05", "18", "bbc_20260518T120000Z.mka")
	newPath := filepath.Join(cacheRoot, "recordings", "bbc", "2026", "05", "18", "bbc_20260518T121500Z.mka")
	for _, path := range []string{oldPath, newPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}

	rec, err := resolveRecordingTarget(cacheRoot, "bbc")
	if err != nil {
		t.Fatalf("resolveRecordingTarget returned error: %v", err)
	}
	if rec.Path != newPath {
		t.Fatalf("resolved recording path = %q, want latest %q", rec.Path, newPath)
	}
}

func TestRecordTranscribeCommandFindsCachedTranscript(t *testing.T) {
	cacheRoot := t.TempDir()
	recordingPath := filepath.Join(cacheRoot, "recordings", "bbc", "2026", "05", "18", "bbc_20260518T120000Z.mka")
	if err := os.MkdirAll(filepath.Dir(recordingPath), 0o755); err != nil {
		t.Fatalf("MkdirAll recording returned error: %v", err)
	}
	if err := os.WriteFile(recordingPath, []byte("audio"), 0o644); err != nil {
		t.Fatalf("WriteFile recording returned error: %v", err)
	}
	rec := recording{
		Station:  "bbc",
		StartUTC: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		Path:     recordingPath,
		Bytes:    5,
	}
	transcriptPath := recordingTranscriptPath(cacheRoot, rec, backendCPU, Config{Model: "tiny", Language: "English"})
	if err := writeRecordingTranscript(transcriptPath, recordingTranscript{
		Version:  transcriptCacheV1,
		Created:  "2026-05-18T12:01:00Z",
		Backend:  backendCPU,
		Model:    "tiny",
		Language: "English",
		Record:   recordingTranscriptRecordingFrom(rec),
		Whisper:  whisperOutput{Text: "hello"},
	}); err != nil {
		t.Fatalf("writeRecordingTranscript returned error: %v", err)
	}

	var out bytes.Buffer
	cfg := Config{CacheDir: cacheRoot, Backend: backendCPU, Model: "tiny", Language: "English"}
	if err := runRecordTranscribe(&out, t.Context(), cfg, "bbc"); err != nil {
		t.Fatalf("runRecordTranscribe returned error: %v", err)
	}

	got := out.String()
	for _, want := range []string{"cached", transcriptCacheV1, backendCPU, "tiny", "English", transcriptPath} {
		if !strings.Contains(got, want) {
			t.Fatalf("record transcribe output missing %q:\n%s", want, got)
		}
	}
}

func TestRecordRestartDelayBacksOffAndCaps(t *testing.T) {
	base := 5 * time.Second
	tests := map[int]time.Duration{
		0:  5 * time.Second,
		1:  5 * time.Second,
		2:  10 * time.Second,
		4:  40 * time.Second,
		10: time.Minute,
	}

	for attempt, want := range tests {
		if got := recordRestartDelay(base, attempt); got != want {
			t.Fatalf("recordRestartDelay(%d) = %s, want %s", attempt, got, want)
		}
	}
}

func TestProgrammeWindowsUseScheduleWeekAndDurationGap(t *testing.T) {
	loc, err := time.LoadLocation(defaultProgrammeTimezone)
	if err != nil {
		t.Fatalf("LoadLocation returned error: %v", err)
	}
	schedule := programmes.Schedule{
		Station:   "TokFM",
		FetchedAt: "2026-05-22T14:00:00Z",
		Entries: []programmes.Entry{
			{DayIndex: 0, Day: "Monday", Time: "20:00", Programme: "Mikrofon TOK FM", Duration: "79 min"},
			{DayIndex: 0, Day: "Monday", Time: "22:00", Programme: "Maciej Zakrocki przedstawia", Duration: "52 min"},
		},
	}

	windows, err := programmeWindows(schedule, loc, ProgrammeTranscribeOptions{WindowMode: "segment"})
	if err != nil {
		t.Fatalf("programmeWindows returned error: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("programme window count = %d, want 2", len(windows))
	}
	if got, want := windows[0].StartUTC.Format(time.RFC3339), "2026-05-18T18:00:00Z"; got != want {
		t.Fatalf("first programme start UTC = %s, want %s", got, want)
	}
	if got, want := windows[0].EndUTC.Format(time.RFC3339), "2026-05-18T19:19:00Z"; got != want {
		t.Fatalf("first programme end UTC = %s, want %s", got, want)
	}
}

func TestProgrammeWindowsGroupScheduleRowsByProgrammeFamily(t *testing.T) {
	loc, err := time.LoadLocation(defaultProgrammeTimezone)
	if err != nil {
		t.Fatalf("LoadLocation returned error: %v", err)
	}
	schedule := programmes.Schedule{
		Station:   "TokFM",
		FetchedAt: "2026-05-22T14:00:00Z",
		Entries: []programmes.Entry{
			{DayIndex: 1, Day: "Tuesday", Time: "05:00", Programme: "Pierwszy Program", Hosts: []string{"Wojciech Muzal"}},
			{DayIndex: 1, Day: "Tuesday", Time: "05:20", Programme: "Pierwszy Program", Duration: "30 min", Hosts: []string{"Wojciech Muzal"}},
			{DayIndex: 1, Day: "Tuesday", Time: "05:40", Programme: "Pierwszy Program", Duration: "4 min", Hosts: []string{"Wojciech Muzal"}},
			{DayIndex: 1, Day: "Tuesday", Time: "07:00", Programme: "Poranek TOK FM - Maciej Kluczka", Hosts: []string{"Maciej Kluczka"}},
		},
	}

	windows, err := programmeWindows(schedule, loc, ProgrammeTranscribeOptions{})
	if err != nil {
		t.Fatalf("programmeWindows returned error: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("programme window count = %d, want 2", len(windows))
	}
	if got, want := windows[0].Entry.Programme, "Pierwszy Program"; got != want {
		t.Fatalf("first grouped programme = %q, want %q", got, want)
	}
	if got, want := windows[0].EndLocal.Format("15:04"), "07:00"; got != want {
		t.Fatalf("first grouped end local = %s, want %s", got, want)
	}
}

func TestDefaultProgrammeTranscribeOptionsRunFullPipeline(t *testing.T) {
	opts := DefaultProgrammeTranscribeOptions()
	if !opts.Diarize {
		t.Fatal("default programme transcribe options should enable diarization")
	}
	if !opts.SpeakerMap {
		t.Fatal("default programme transcribe options should enable speaker mapping and cleanup")
	}
}

func TestProgrammeDiarizationPathIncludesModel(t *testing.T) {
	loc, err := time.LoadLocation(defaultProgrammeTimezone)
	if err != nil {
		t.Fatalf("LoadLocation returned error: %v", err)
	}
	cacheRoot := t.TempDir()
	window := programmeWindow{
		Station:    "tokfm",
		StartLocal: time.Date(2026, 5, 19, 5, 0, 0, 0, loc),
		ID:         "0500-pierwszy-program",
	}

	got := programmeDiarizationPath(cacheRoot, window, loc, "pyannote/speaker-diarization-community-1", "mps")
	want := filepath.Join(cacheRoot, "programmes", "diarization", programmeDiarizationCacheV1, "tokfm", "pyannote-speaker-diarization-community-1", "mps", "2026", "05", "19", "0500-pierwszy-program.json")
	if got != want {
		t.Fatalf("programmeDiarizationPath = %q, want %q", got, want)
	}
}

func TestSpeakerMapPromptUsesBoundedExcerpts(t *testing.T) {
	window := programmeWindow{
		StartLocal: time.Date(2026, 5, 19, 7, 0, 0, 0, time.UTC),
		Entry: programmes.Entry{
			Programme: "Poranek TOK FM",
			Hosts:     []string{"Jan Nowak"},
		},
	}
	var paragraphs []diarizedParagraph
	for i := 0; i < 100; i++ {
		speaker := "SPEAKER_00"
		if i%2 == 1 {
			speaker = "SPEAKER_01"
		}
		paragraphs = append(paragraphs, diarizedParagraph{
			Speaker: speaker,
			Text:    strings.Repeat("bardzo dlugi fragment ", 80),
		})
	}

	prompt := speakerMapPrompt(window, paragraphs)
	if len(prompt) > 26000 {
		t.Fatalf("speaker map prompt length = %d, want bounded prompt", len(prompt))
	}
	if !strings.Contains(prompt, "[transcript excerpt truncated]") {
		t.Fatalf("speaker map prompt should note transcript truncation:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Representative samples by diarization label") {
		t.Fatalf("speaker map prompt missing speaker samples:\n%s", prompt)
	}
}

func TestProgrammeAdPostprocessPromptRemovesAdsAndMusic(t *testing.T) {
	window := programmeWindow{
		StartLocal: time.Date(2026, 5, 19, 7, 0, 0, 0, time.UTC),
		Entry:      programmes.Entry{Programme: "Poranek TOK FM"},
	}
	prompt := programmeAdPostprocessPrompt(window, []programmeMarkdownParagraph{
		{ID: 1, Speaker: "Jan Nowak", Text: "Wracamy po przerwie.", Start: 0, End: 3},
	})

	for _, want := range []string{"any language", "reklama", "autopromocja", "advertisement", "songs", "sung lyrics", "music beds", "If unsure, keep it"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("programmeAdPostprocessPrompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSpeakerMapSamplesSkipLikelyNonProgrammeAudio(t *testing.T) {
	paragraphs := []diarizedParagraph{
		{Speaker: "SPEAKER_00", Text: "No one knows what it's like to be the bad man"},
		{Speaker: "SPEAKER_01", Text: "Zapraszamy do sklepu. Promocja tylko dzisiaj."},
		{Speaker: "SPEAKER_02", Text: "Dzień dobry, Jan Nowak, dzisiaj rozmawiamy o polityce."},
	}

	samples := speakerMapSamples(paragraphs, 3, 600, 45)
	if len(samples) != 1 {
		t.Fatalf("speakerMapSamples count = %d, want 1: %#v", len(samples), samples)
	}
	if samples[0].Speaker != "SPEAKER_02" {
		t.Fatalf("speakerMapSamples speaker = %q, want SPEAKER_02", samples[0].Speaker)
	}
}

func TestParseProgrammeAdDecision(t *testing.T) {
	decision, err := parseProgrammeAdDecision(`{"remove":[{"id":2,"reason":"autopromo"}],"rewrites":[{"id":3,"text":"zostaje","reason":"embedded ad"}]}`)
	if err != nil {
		t.Fatalf("parseProgrammeAdDecision returned error: %v", err)
	}
	if len(decision.Remove) != 1 || decision.Remove[0].ID != 2 {
		t.Fatalf("remove decisions = %#v, want id 2", decision.Remove)
	}
	if len(decision.Rewrites) != 1 || decision.Rewrites[0].Text != "zostaje" {
		t.Fatalf("rewrite decisions = %#v, want rewritten text", decision.Rewrites)
	}
}

func TestParseProgrammeAdDecisionAcceptsNumericRemoveIDs(t *testing.T) {
	decision, err := parseProgrammeAdDecision(`{"remove":[2,3],"rewrites":null}`)
	if err != nil {
		t.Fatalf("parseProgrammeAdDecision returned error: %v", err)
	}
	if len(decision.Remove) != 2 || decision.Remove[0].ID != 2 || decision.Remove[1].ID != 3 {
		t.Fatalf("remove decisions = %#v, want numeric ids 2 and 3", decision.Remove)
	}
	if decision.Remove[0].Reason == "" {
		t.Fatalf("numeric remove decision should get a default reason: %#v", decision.Remove)
	}
}

func TestMarkMusicBridgeFragmentsRemovesShortLyricsBoundary(t *testing.T) {
	paragraphs := []programmeMarkdownParagraph{
		{ID: 1, Speaker: "Host", Text: "Behind Blue Eyes."},
		{ID: 2, Speaker: "UNKNOWN", Text: "Dzień dobry."},
		{ID: 3, Speaker: "SPEAKER_39", Text: "No one knows what it's like."},
	}
	removed := map[int]programmeRemovedBlock{
		3: {ID: 3, Reason: "song lyrics", Text: paragraphs[2].Text},
	}

	markMusicBridgeFragments(paragraphs, removed)
	if _, ok := removed[2]; !ok {
		t.Fatalf("short bridge before song lyrics was not removed: %#v", removed)
	}
	if _, ok := removed[1]; ok {
		t.Fatalf("host intro should not be removed: %#v", removed)
	}
}

func TestProgrammeMarkdownFromParagraphsCutsRemovedAds(t *testing.T) {
	window := programmeWindow{Entry: programmes.Entry{Programme: "Programme"}}
	markdown := programmeMarkdownFromParagraphs(window, []programmeMarkdownParagraph{
		{ID: 1, Speaker: "Jan Nowak", Text: "Program content."},
		{ID: 3, Speaker: "Jan Nowak", Text: "Back to content."},
	})
	if strings.Contains(strings.ToLower(markdown), "reklama") {
		t.Fatalf("markdown still contains ad text:\n%s", markdown)
	}
	for _, want := range []string{"# Programme", "Jan Nowak: Program content.", "Jan Nowak: Back to content."} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}
}

func TestEnsureRecordingDateDirsCreatesUpcomingUTCDays(t *testing.T) {
	stationRoot := t.TempDir()
	now := time.Date(2026, 5, 18, 23, 30, 0, 0, time.UTC)

	if err := ensureRecordingDateDirs(stationRoot, now); err != nil {
		t.Fatalf("ensureRecordingDateDirs returned error: %v", err)
	}

	for _, want := range []string{
		filepath.Join(stationRoot, "2026", "05", "18"),
		filepath.Join(stationRoot, "2026", "05", "19"),
		filepath.Join(stationRoot, "2026", "05", "20"),
	} {
		info, err := os.Stat(want)
		if err != nil {
			t.Fatalf("expected date dir %s: %v", want, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", want)
		}
	}
}

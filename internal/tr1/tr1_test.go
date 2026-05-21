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

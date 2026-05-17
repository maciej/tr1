package tr1

import (
	"bytes"
	"os"
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
	cfg := config{station: "bbc", language: "Polish"}

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
	if cfg.language != "English" {
		t.Fatalf("language = %q, want English", cfg.language)
	}
}

func TestApplySelectedStationDefaultsKeepsLanguageOverride(t *testing.T) {
	cfg := config{station: "bbc", language: "Spanish", languageSet: true}

	if _, _, err := applySelectedStationDefaults(&cfg); err != nil {
		t.Fatalf("applySelectedStationDefaults returned error: %v", err)
	}
	if cfg.language != "Spanish" {
		t.Fatalf("language = %q, want explicit override Spanish", cfg.language)
	}
}

func TestApplySelectedStationDefaultsSkipsCustomStream(t *testing.T) {
	cfg := config{
		station:   "bbc",
		streamURL: "https://example.com/custom.mp3",
		language:  "Polish",
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
	if cfg.language != "Polish" {
		t.Fatalf("language = %q, want unchanged Polish", cfg.language)
	}
}

func TestStreamSelectionCustomURL(t *testing.T) {
	gotName, gotURL, err := streamSelection(config{
		station:   "rmf",
		streamURL: "https://example.com/custom.mp3",
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

	if err := printStableWords(t.Context(), config{}, writer, resp, 10, &printedUntil); err != nil {
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

	if err := printStableWords(t.Context(), config{}, writer, resp, 10, &printedUntil); err != nil {
		t.Fatalf("printStableWords returned error: %v", err)
	}

	want := "znów furmanko \ntam "
	if got := out.String(); got != want {
		t.Fatalf("printed output = %q, want %q", got, want)
	}
}

func TestAudioMonitorCommandConsumesPCMFromStdin(t *testing.T) {
	cmd := audioMonitorCommand(t.Context(), config{ffplayBin: "ffplay"})

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

func TestStationsCommandListsAvailableStations(t *testing.T) {
	cmd := NewRootCommand(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"stations"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("stations command returned error: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"ALIAS",
		"NAME",
		"LANGUAGE",
		"URL",
		"tokfm",
		"TokFM",
		"Polish",
		"bbc",
		"BBC World Service",
		"English",
		"trojka, trójka, pr3, program3, program-3, troika, radio3, radio-3, three, 3",
		"https://rs201-krk-cyfronet.rmfstream.pl/rmf_fm",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stations output missing %q:\n%s", want, got)
		}
	}
}

func TestStationsCommandRejectsArgs(t *testing.T) {
	cmd := NewRootCommand(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"stations", "rmf"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("stations command accepted unexpected argument")
	}
}

func TestLabExecutableOwnsNonEndUserCommands(t *testing.T) {
	cmd := NewLabCommand(t.Context())

	if found, _, err := cmd.Find([]string{"preview"}); err != nil || found == nil || found.Name() != "preview" {
		t.Fatalf("preview command lookup failed: command=%v err=%v", found, err)
	}
	if found, _, err := cmd.Find([]string{"benchmark"}); err != nil || found == nil || found.Name() != "benchmark" {
		t.Fatalf("benchmark command lookup failed: command=%v err=%v", found, err)
	}
	if found, _, err := cmd.Find([]string{"preview-local"}); err == nil && found != nil && found.Name() == "preview-local" {
		t.Fatal("preview-local command is still registered")
	}
}

func TestRootCommandExcludesNonEndUserCommands(t *testing.T) {
	cmd := NewRootCommand(t.Context())

	if found, _, err := cmd.Find([]string{"lab"}); err == nil && found != nil && found.Name() == "lab" {
		t.Fatal("lab command is still registered on tr1")
	}
	if found, _, err := cmd.Find([]string{"preview"}); err == nil && found != nil && found.Name() == "preview" {
		t.Fatal("preview command is still registered on tr1")
	}
	if found, _, err := cmd.Find([]string{"benchmark"}); err == nil && found != nil && found.Name() == "benchmark" {
		t.Fatal("benchmark command is still registered on tr1")
	}
}

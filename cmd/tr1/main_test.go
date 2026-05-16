package main

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
	cmd := newRootCommand(t.Context())
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
		"URL",
		"tokfm",
		"TokFM",
		"trojka, trójka, pr3, program3, program-3, troika, radio3, radio-3, three, 3",
		"https://rs201-krk-cyfronet.rmfstream.pl/rmf_fm",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stations output missing %q:\n%s", want, got)
		}
	}
}

func TestStationsCommandRejectsArgs(t *testing.T) {
	cmd := newRootCommand(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"stations", "rmf"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("stations command accepted unexpected argument")
	}
}

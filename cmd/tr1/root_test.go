package main

import (
	"bytes"
	"strings"
	"testing"
)

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
	cmd := newRootCommand(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"stations", "rmf"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("stations command accepted unexpected argument")
	}
}

func TestRootCommandExcludesNonEndUserCommands(t *testing.T) {
	cmd := newRootCommand(t.Context())

	if found, _, err := cmd.Find([]string{"lab"}); err == nil && found != nil && found.Name() == "lab" {
		t.Fatal("lab command is registered on tr1")
	}
	if found, _, err := cmd.Find([]string{"preview"}); err == nil && found != nil && found.Name() == "preview" {
		t.Fatal("preview command is registered on tr1")
	}
	if found, _, err := cmd.Find([]string{"benchmark"}); err == nil && found != nil && found.Name() == "benchmark" {
		t.Fatal("benchmark command is registered on tr1")
	}
	if found, _, err := cmd.Find([]string{"programmes"}); err == nil && found != nil && found.Name() == "programmes" {
		t.Fatal("programmes command is registered on tr1")
	}
}

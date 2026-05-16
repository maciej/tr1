package main

import "testing"

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
		"":                  "news-preview",
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

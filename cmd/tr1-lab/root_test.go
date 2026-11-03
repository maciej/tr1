package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tr1/internal/tr1"
)

func TestLabExecutableOwnsNonEndUserCommands(t *testing.T) {
	cmd := newLabCommand(t.Context())

	if found, _, err := cmd.Find([]string{"preview"}); err != nil || found == nil || found.Name() != "preview" {
		t.Fatalf("preview command lookup failed: command=%v err=%v", found, err)
	}
	if found, _, err := cmd.Find([]string{"benchmark"}); err != nil || found == nil || found.Name() != "benchmark" {
		t.Fatalf("benchmark command lookup failed: command=%v err=%v", found, err)
	}
	if found, _, err := cmd.Find([]string{"record"}); err != nil || found == nil || found.Name() != "record" {
		t.Fatalf("record command lookup failed: command=%v err=%v", found, err)
	}
	if found, _, err := cmd.Find([]string{"record", "list"}); err != nil || found == nil || found.Name() != "list" {
		t.Fatalf("record list command lookup failed: command=%v err=%v", found, err)
	}
	if found, _, err := cmd.Find([]string{"programmes"}); err != nil || found == nil || found.Name() != "programmes" {
		t.Fatalf("programmes command lookup failed: command=%v err=%v", found, err)
	}
	if found, _, err := cmd.Find([]string{"programmes", "transcribe"}); err != nil || found == nil || found.Name() != "transcribe" {
		t.Fatalf("programmes transcribe command lookup failed: command=%v err=%v", found, err)
	}
	if found, _, err := cmd.Find([]string{"preview-local"}); err == nil && found != nil && found.Name() == "preview-local" {
		t.Fatal("preview-local command is still registered")
	}
}

func writeTestFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func TestValidateProgrammesConfigRejectsUnsupportedStation(t *testing.T) {
	cfg := tr1.DefaultConfig()
	cfg.Station = "rmf"

	if err := validateProgrammesConfig(cfg, "", "table", time.Second); err == nil {
		t.Fatal("validateProgrammesConfig accepted unsupported station")
	}
}

func TestValidateProgrammesConfigAcceptsBBC(t *testing.T) {
	cfg := tr1.DefaultConfig()
	cfg.Station = "bbc"

	if err := validateProgrammesConfig(cfg, "", "table", time.Second); err != nil {
		t.Fatalf("validateProgrammesConfig rejected BBC: %v", err)
	}
}

func TestProgrammesTranscribeAcceptsBBCSchedule(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "data": [
    {
      "id": "p0nf8w5y",
      "urn": "urn:bbc:radio:episode:w1730nkmgdxjpp1",
      "start": "2026-05-24T00:00:00Z",
      "end": "2026-05-24T00:06:00Z",
      "duration": 360,
      "titles": {"primary": "BBC News", "secondary": "24/05/2026 00:01 GMT"},
      "container": {"id": "p002vsmz", "title": "BBC News"}
    }
  ]
}`))
	}))
	defer server.Close()

	cmd := newLabCommand(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"programmes", "transcribe", "bbc",
		"--source-url", server.URL + "?date=2026-05-24",
		"--cache-dir", t.TempDir(),
		"--plan-only",
		"--diarize=false",
		"--speaker-map=false",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("programmes transcribe bbc returned error: %v", err)
	}
	if !strings.Contains(out.String(), "STATUS") {
		t.Fatalf("programmes transcribe bbc output missing summary header:\n%s", out.String())
	}
}

func TestRecordListCommandListsStationRecordings(t *testing.T) {
	cacheRoot := t.TempDir()
	tokPath := filepath.Join(cacheRoot, "recordings", "tokfm", "2026", "05", "18", "tokfm_20260518T120000Z.mka")
	rmfPath := filepath.Join(cacheRoot, "recordings", "rmf", "2026", "05", "18", "rmf_20260518T130000Z.mka")
	for _, path := range []string{tokPath, rmfPath} {
		if err := writeTestFile(path, []byte("audio")); err != nil {
			t.Fatalf("writeTestFile returned error: %v", err)
		}
	}

	cmd := newLabCommand(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"record", "list", "--cache-dir", cacheRoot, "tok"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("record list command returned error: %v", err)
	}

	got := out.String()
	for _, want := range []string{"START_UTC", "STATION", "BYTES", "PATH", "2026-05-18T12:00:00Z", "tokfm", tokPath} {
		if !strings.Contains(got, want) {
			t.Fatalf("record list output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, rmfPath) {
		t.Fatalf("record list output included another station:\n%s", got)
	}
}

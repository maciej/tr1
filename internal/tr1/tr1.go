package tr1

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
)

const (
	defaultStationAlias  = "tokfm"
	defaultWorkDir       = ".tr1"
	backendAuto          = "auto"
	backendCPU           = "cpu"
	backendMLX           = "mlx"
	benchmarkModeWhole   = "whole"
	benchmarkModeChunked = "chunked"
	defaultMLXModelRepo  = "mlx-community/whisper-%s-mlx"
	defaultFixtureName   = "biebrza-broadcast"
	defaultRecordExt     = ".mka"
	transcriptCacheV1    = "transcribe-v1"
)

const defaultRecordSegmentDuration = 15 * time.Minute

type benchmarkFixture struct {
	Name           string
	TranscriptPath string
	AudioPath      string
}

var benchmarkFixtures = []benchmarkFixture{
	{
		Name:           "news-preview",
		TranscriptPath: filepath.Join("fixtures", "news-preview.txt"),
		AudioPath:      filepath.Join("assets", "news-preview.mp3"),
	},
	{
		Name:           "biebrza-broadcast",
		TranscriptPath: filepath.Join("fixtures", "biebrza-broadcast.txt"),
		AudioPath:      filepath.Join("assets", "biebrza-broadcast.mp3"),
	},
}

type Station struct {
	Name     string
	URL      string
	Language string
	Aliases  []string
}

type recording struct {
	Station  string
	StartUTC time.Time
	Path     string
	Bytes    int64
}

type recordingTranscript struct {
	Version  string                       `json:"version"`
	Created  string                       `json:"created_at"`
	Backend  string                       `json:"backend"`
	Model    string                       `json:"model"`
	Language string                       `json:"language"`
	Record   recordingTranscriptRecording `json:"recording"`
	Whisper  whisperOutput                `json:"whisper"`
}

type recordingTranscriptRecording struct {
	Station  string `json:"station"`
	StartUTC string `json:"start_utc,omitempty"`
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes,omitempty"`
}

var stations = []Station{
	{
		Name:     "TokFM",
		URL:      "http://www.tuba.fm/stream.pls?radio=10&mp3=1",
		Language: "Polish",
		Aliases:  []string{"tokfm", "tok", "tok-fm"},
	},
	{
		Name:     "Polskie Radio Jedynka",
		URL:      "http://stream3.polskieradio.pl:8900/listen.pls",
		Language: "Polish",
		Aliases:  []string{"jedynka", "pr1", "program1", "program-1", "radio1", "radio-1", "one", "1"},
	},
	{
		Name:     "Program Drugi Polskiego Radia",
		URL:      "http://stream3.polskieradio.pl:8902/listen.pls",
		Language: "Polish",
		Aliases:  []string{"dwojka", "dwójka", "pr2", "program2", "program-2", "drugi", "radio2", "radio-2", "two", "2"},
	},
	{
		Name:     "Trójka",
		URL:      "http://stream3.polskieradio.pl:8904/listen.pls",
		Language: "Polish",
		Aliases:  []string{"trojka", "trójka", "pr3", "program3", "program-3", "troika", "radio3", "radio-3", "three", "3"},
	},
	{
		Name:     "RMF FM",
		URL:      "https://rs201-krk-cyfronet.rmfstream.pl/rmf_fm",
		Language: "Polish",
		Aliases:  []string{"rmf", "rmffm", "rmf-fm"},
	},
	{
		Name:     "Radio ZET",
		URL:      "https://playerservices.streamtheworld.com/api/livestream-redirect/RADIO_ZET_SC",
		Language: "Polish",
		Aliases:  []string{"zet", "radiozet", "radio-zet"},
	},
	{
		Name:     "BBC World Service",
		URL:      "https://stream.live.vc.bbcmedia.co.uk/bbc_world_service",
		Language: "English",
		Aliases:  []string{"bbc", "bbcws", "bbc-world-service", "worldservice", "world-service", "english"},
	},
}

//go:embed mlx.pyproject.toml
var embeddedPyproject string

//go:embed live_whisper_worker.py
var liveWhisperWorkerScript string

//go:embed live_mlx_worker.py
var liveMLXWorkerScript string

//go:embed mlx_transcribe.py
var mlxTranscribeScript string

type Config struct {
	Station         string
	StreamURL       string
	Model           string
	Models          string
	Language        string
	LanguageSet     bool
	Backend         string
	ChunkSeconds    int
	StepSeconds     int
	Holdback        time.Duration
	WorkDir         string
	WhisperBin      string
	UVBin           string
	PythonBin       string
	FFmpegBin       string
	FFplayBin       string
	CacheDir        string
	SagBin          string
	SagVoice        string
	Fixture         string
	WordDelay       time.Duration
	RecordSegment   time.Duration
	RecordRestart   time.Duration
	TranscribeForce bool
	Spinner         bool
	PreviewOut      string
	BenchmarkMode   string
	InitialPrompt   string
	MonitorAudio    bool
	Verbose         bool
}

type whisperOutput struct {
	Text     string           `json:"text"`
	Segments []whisperSegment `json:"segments"`
}

type whisperSegment struct {
	Text  string        `json:"text"`
	Words []whisperWord `json:"words"`
}

type whisperWord struct {
	Word        string  `json:"word"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Probability float64 `json:"probability"`
}

type whisperRequest struct {
	Seq           int     `json:"seq"`
	Offset        float64 `json:"offset"`
	Duration      float64 `json:"duration"`
	SampleRate    int     `json:"sample_rate"`
	PCM16Base64   string  `json:"pcm16_b64"`
	InitialPrompt string  `json:"initial_prompt,omitempty"`
}

type whisperResponse struct {
	Seq      int           `json:"seq"`
	Offset   float64       `json:"offset"`
	Duration float64       `json:"duration"`
	Text     string        `json:"text"`
	Words    []whisperWord `json:"words"`
	Error    string        `json:"error,omitempty"`
}

type benchResult struct {
	Backend  string
	Model    string
	WER      float64
	Words    int
	Duration time.Duration
	RTF      float64
	Text     string
}

func defaultConfig() Config {
	return Config{
		Station:       getenv("TR1_STATION", defaultStationAlias),
		StreamURL:     getenv("TR1_STREAM_URL", ""),
		Model:         getenv("TR1_MODEL", "medium"),
		Models:        getenv("TR1_MODELS", "tiny,base"),
		Language:      getenv("TR1_LANGUAGE", "Polish"),
		LanguageSet:   envHasValue("TR1_LANGUAGE"),
		Backend:       getenv("TR1_BACKEND", backendAuto),
		ChunkSeconds:  intFromEnv("TR1_CHUNK_SECONDS", 24),
		StepSeconds:   intFromEnv("TR1_STEP_SECONDS", 12),
		Holdback:      durationFromEnv("TR1_HOLDBACK", time.Second),
		WorkDir:       getenv("TR1_WORKDIR", defaultWorkDir),
		WhisperBin:    getenv("TR1_WHISPER_BIN", "whisper"),
		UVBin:         getenv("TR1_UV_BIN", "uv"),
		PythonBin:     getenv("TR1_PYTHON_BIN", ""),
		FFmpegBin:     getenv("TR1_FFMPEG_BIN", "ffmpeg"),
		FFplayBin:     getenv("TR1_FFPLAY_BIN", "ffplay"),
		CacheDir:      getenv("TR1_CACHE_DIR", ""),
		SagBin:        getenv("TR1_SAG_BIN", "sag"),
		SagVoice:      getenv("TR1_SAG_VOICE", ""),
		Fixture:       getenv("TR1_FIXTURE", defaultFixtureName),
		WordDelay:     durationFromEnv("TR1_WORD_DELAY", 35*time.Millisecond),
		RecordSegment: durationFromEnv("TR1_RECORD_SEGMENT_DURATION", defaultRecordSegmentDuration),
		RecordRestart: durationFromEnv("TR1_RECORD_RESTART_DELAY", 5*time.Second),
		Spinner:       boolFromEnv("TR1_SPINNER", true),
		PreviewOut:    "",
		BenchmarkMode: getenv("TR1_BENCHMARK_MODE", benchmarkModeWhole),
		InitialPrompt: "Polski serwis informacyjny radiowy. Poprawna polska interpunkcja i nazwy własne.",
		MonitorAudio:  boolFromEnv("TR1_PLAY", false),
		Verbose:       boolFromEnv("TR1_VERBOSE", false),
	}
}

func DefaultConfig() Config {
	return defaultConfig()
}

func Stations() []Station {
	out := make([]Station, len(stations))
	for i, s := range stations {
		out[i] = s
		out[i].Aliases = append([]string(nil), s.Aliases...)
	}
	return out
}

func LookupStation(query string) (Station, error) {
	return lookupStation(query)
}

func StationHelp() string {
	return stationHelp()
}

func FixtureHelp() string {
	return fixtureHelp()
}

func WriteStations(w io.Writer) error {
	return writeStations(w)
}

func ValidateStreamConfig(cfg Config) error {
	return validateStreamConfig(cfg)
}

func ValidateRecordConfig(cfg Config) error {
	return validateRecordConfig(cfg)
}

func ValidateRecordTranscribeConfig(cfg Config) error {
	return validateRecordTranscribeConfig(cfg)
}

func RunStream(ctx context.Context, cfg Config) error {
	return runStream(ctx, cfg)
}

func RunPreview(ctx context.Context, cfg Config) error {
	return runPreview(ctx, cfg)
}

func RunBenchmark(ctx context.Context, cfg Config) error {
	return runBenchmark(ctx, cfg)
}

func RunRecord(ctx context.Context, cfg Config) error {
	return runRecord(ctx, cfg)
}

func RunRecordList(w io.Writer, cfg Config) error {
	return runRecordList(w, cfg)
}

func RunRecordTranscribe(w io.Writer, ctx context.Context, cfg Config, target string) error {
	return runRecordTranscribe(w, ctx, cfg, target)
}

func CacheRoot(cacheDir string) (string, error) {
	return recordingCacheRoot(Config{CacheDir: cacheDir})
}

func validateStreamConfig(cfg Config) error {
	if err := validateBackend(cfg.Backend); err != nil {
		return err
	}
	if cfg.ChunkSeconds < 3 {
		return fmt.Errorf("--window must be at least 3 seconds")
	}
	if cfg.StepSeconds < 1 {
		return fmt.Errorf("--step must be at least 1 second")
	}
	if cfg.StepSeconds > cfg.ChunkSeconds {
		return fmt.Errorf("--step must be less than or equal to --window")
	}
	if cfg.MonitorAudio {
		if err := requireBinaries(cfg.FFplayBin); err != nil {
			return err
		}
	}
	return nil
}

func validateRecordConfig(cfg Config) error {
	if cfg.RecordSegment < time.Second {
		return fmt.Errorf("--segment-duration must be at least 1s")
	}
	if cfg.RecordRestart < time.Second {
		return fmt.Errorf("--restart-delay must be at least 1s")
	}
	return requireBinaries(cfg.FFmpegBin)
}

func validateRecordTranscribeConfig(cfg Config) error {
	if err := validateBackend(cfg.Backend); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("--model must not be empty")
	}
	return nil
}

func runRecord(ctx context.Context, cfg Config) error {
	selectedStation, stationURL, err := applySelectedStationDefaults(&cfg)
	if err != nil {
		return err
	}
	streamURL, err := resolveStreamURL(ctx, stationURL)
	if err != nil {
		return err
	}
	cacheRoot, err := recordingCacheRoot(cfg)
	if err != nil {
		return err
	}
	stationSlug := recordingStationSlug(cfg, selectedStation)
	pattern := recordingSegmentPattern(cacheRoot, stationSlug)
	stationRoot := recordingStationRoot(cacheRoot, stationSlug)
	if err := os.MkdirAll(stationRoot, 0o755); err != nil {
		return err
	}
	stopDateDirs, err := startRecordingDateDirMaintainer(ctx, stationRoot)
	if err != nil {
		return err
	}
	defer stopDateDirs()

	fmt.Fprintf(os.Stderr, "recording %s into %s\n", selectedStation, stationRoot)
	fmt.Fprintf(os.Stderr, "chunks: %s, timestamps: UTC, stop with Ctrl+C\n", cfg.RecordSegment)
	status(cfg, "stream", "using "+streamURL)

	attempt := 0
	for {
		started := time.Now()
		err := runRecordFFmpeg(ctx, cfg, streamURL, pattern)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(started) > time.Minute {
			attempt = 0
		} else {
			attempt++
		}
		delay := recordRestartDelay(cfg.RecordRestart, attempt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ffmpeg stopped: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "ffmpeg stopped without an error")
		}
		fmt.Fprintf(os.Stderr, "restarting in %s\n", delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func runRecordFFmpeg(ctx context.Context, cfg Config, streamURL, pattern string) error {
	seconds := int(cfg.RecordSegment.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	args := []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_at_eof", "1",
		"-reconnect_on_network_error", "1",
		"-reconnect_on_http_error", "5xx",
		"-reconnect_delay_max", "30",
		"-i", streamURL,
		"-map", "0:a:0",
		"-vn", "-sn", "-dn",
		"-c:a", "copy",
		"-f", "segment",
		"-segment_time", strconv.Itoa(seconds),
		"-segment_atclocktime", "1",
		"-segment_format", "matroska",
		"-reset_timestamps", "1",
		"-strftime", "1",
		pattern,
	}
	cmd := exec.Command(cfg.FFmpegBin, args...)
	cmd.Env = append(os.Environ(), "TZ=UTC")
	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(&stderr, prefixedStderr(cfg, "ffmpeg"))
	if cfg.Verbose {
		cmd.Stdout = os.Stderr
	} else {
		cmd.Stdout = io.Discard
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	err := waitForRecordFFmpeg(ctx, cmd)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%w: %s", err, oneLine(msg))
		}
		return err
	}
	return nil
}

func waitForRecordFFmpeg(ctx context.Context, cmd *exec.Cmd) error {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		return ctx.Err()
	}
}

func runRecordList(w io.Writer, cfg Config) error {
	cacheRoot, err := recordingCacheRoot(cfg)
	if err != nil {
		return err
	}
	recordings, err := listRecordings(cacheRoot, cfg.Station)
	if err != nil {
		return err
	}
	return writeRecordings(w, recordings)
}

func runRecordTranscribe(w io.Writer, ctx context.Context, cfg Config, target string) error {
	cacheRoot, err := recordingCacheRoot(cfg)
	if err != nil {
		return err
	}
	rec, err := resolveRecordingTarget(cacheRoot, target)
	if err != nil {
		return err
	}
	applyRecordingLanguageDefaults(&cfg, rec)
	requestedBackend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if requestedBackend != backendAuto {
		outPath := recordingTranscriptPath(cacheRoot, rec, requestedBackend, cfg)
		if !cfg.TranscribeForce {
			if _, err := os.Stat(outPath); err == nil {
				return writeRecordingTranscriptSummary(w, "cached", outPath, requestedBackend, cfg)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if err := ensureDirs(cfg); err != nil {
		return err
	}
	backend, err := prepareBackend(ctx, &cfg)
	if err != nil {
		return err
	}
	outPath := recordingTranscriptPath(cacheRoot, rec, backend, cfg)
	if !cfg.TranscribeForce {
		if _, err := os.Stat(outPath); err == nil {
			return writeRecordingTranscriptSummary(w, "cached", outPath, backend, cfg)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	status(cfg, "recording", "transcribing "+rec.Path)
	out, err := transcribe(ctx, cfg, cfg.Model, rec.Path)
	if err != nil {
		return err
	}
	envelope := recordingTranscript{
		Version:  transcriptCacheV1,
		Created:  time.Now().UTC().Format(time.RFC3339Nano),
		Backend:  backend,
		Model:    cfg.Model,
		Language: cfg.Language,
		Record:   recordingTranscriptRecordingFrom(rec),
		Whisper:  out,
	}
	if err := writeRecordingTranscript(outPath, envelope); err != nil {
		return err
	}
	return writeRecordingTranscriptSummary(w, "transcribed", outPath, backend, cfg)
}

func resolveRecordingTarget(cacheRoot, target string) (recording, error) {
	if strings.TrimSpace(target) == "" {
		return recording{}, fmt.Errorf("recording path or station is required")
	}
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		path, err := filepath.Abs(target)
		if err != nil {
			return recording{}, err
		}
		if rec, ok, err := recordingFromPath(filepath.Join(cacheRoot, "recordings"), path); err != nil {
			return recording{}, err
		} else if ok {
			return rec, nil
		}
		return recording{
			Station:  stationFromRecordingFilename(path),
			Path:     path,
			Bytes:    info.Size(),
			StartUTC: recordingStartFromFilename(path),
		}, nil
	}
	recordings, err := listRecordings(cacheRoot, target)
	if err != nil {
		return recording{}, err
	}
	if len(recordings) == 0 {
		return recording{}, fmt.Errorf("no recordings found for %q", target)
	}
	return recordings[len(recordings)-1], nil
}

func applyRecordingLanguageDefaults(cfg *Config, rec recording) {
	if cfg.LanguageSet {
		return
	}
	if selected, err := lookupStation(rec.Station); err == nil && selected.Language != "" {
		cfg.Language = selected.Language
	}
}

func recordingTranscriptRecordingFrom(rec recording) recordingTranscriptRecording {
	out := recordingTranscriptRecording{
		Station: rec.Station,
		Path:    rec.Path,
		Bytes:   rec.Bytes,
	}
	if !rec.StartUTC.IsZero() {
		out.StartUTC = rec.StartUTC.Format(time.RFC3339)
	}
	return out
}

func writeRecordingTranscript(path string, transcript recordingTranscript) error {
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

func writeRecordingTranscriptSummary(w io.Writer, statusText, path, backend string, cfg Config) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "STATUS\tVERSION\tBACKEND\tMODEL\tLANGUAGE\tPATH"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", statusText, transcriptCacheV1, backend, cfg.Model, cfg.Language, path); err != nil {
		return err
	}
	return tw.Flush()
}

func recordRestartDelay(base time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for i := 1; i < attempt && delay < time.Minute; i++ {
		delay *= 2
	}
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func startRecordingDateDirMaintainer(ctx context.Context, stationRoot string) (func(), error) {
	if err := ensureRecordingDateDirs(stationRoot, time.Now().UTC()); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_ = ensureRecordingDateDirs(stationRoot, now.UTC())
			}
		}
	}()
	return cancel, nil
}

func ensureRecordingDateDirs(stationRoot string, now time.Time) error {
	day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	for offset := 0; offset <= 2; offset++ {
		t := day.AddDate(0, 0, offset)
		dir := filepath.Join(stationRoot, t.Format("2006"), t.Format("01"), t.Format("02"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func runStream(ctx context.Context, cfg Config) error {
	selectedStation, stationURL, err := applySelectedStationDefaults(&cfg)
	if err != nil {
		return err
	}
	backend, err := prepareBackend(ctx, &cfg)
	if err != nil {
		return err
	}
	if err := ensureDirs(cfg); err != nil {
		return err
	}

	streamURL, err := resolveStreamURL(ctx, stationURL)
	if err != nil {
		return err
	}
	status(cfg, "station", selectedStation)
	status(cfg, "stream", "using "+streamURL)
	status(cfg, "backend", backend)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return streamToWhisper(ctx, cfg, streamURL)
}

func streamToWhisper(ctx context.Context, cfg Config, streamURL string) error {
	ffmpegStdout, ffmpegDone, err := startPCMStream(ctx, cfg, streamURL)
	if err != nil {
		return err
	}
	audioMonitor, err := startAudioMonitor(ctx, cfg)
	if err != nil {
		return err
	}
	defer audioMonitor.close()
	worker, err := startWhisperWorker(cfg)
	if err != nil {
		return err
	}
	defer worker.close()

	const sampleRate = 16000
	const bytesPerSample = 2
	windowBytes := cfg.ChunkSeconds * sampleRate * bytesPerSample
	stepBytes := cfg.StepSeconds * sampleRate * bytesPerSample
	holdbackSeconds := cfg.Holdback.Seconds()

	status(cfg, "ffmpeg", "streaming raw 16 kHz PCM")
	status(cfg, "whisper", fmt.Sprintf("streaming %ds rolling windows every %ds", cfg.ChunkSeconds, cfg.StepSeconds))

	wordWriter := newTerminalWordWriter(os.Stdout, cfg.Spinner)
	defer wordWriter.close()
	if err := wordWriter.tickTuning(); err != nil {
		return err
	}

	var pcm []byte
	readBuf := make([]byte, 4096)
	nextSubmit := stepBytes
	seq := 0
	totalBytes := 0
	printedUntil := 0.0
	transcriptStarted := false
	pending := map[int]whisperResponse{}
	nextPrintSeq := 1

	for {
		n, readErr := ffmpegStdout.Read(readBuf)
		if n > 0 {
			chunk := readBuf[:n]
			if err := audioMonitor.write(chunk); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return err
			}
			pcm = append(pcm, chunk...)
			totalBytes += n
			for totalBytes >= nextSubmit && len(pcm) >= minInt(windowBytes, totalBytes) {
				seq++
				window := pcm
				if len(window) > windowBytes {
					window = window[len(window)-windowBytes:]
				}
				offsetBytes := totalBytes - len(window)
				req := whisperRequest{
					Seq:           seq,
					Offset:        float64(offsetBytes) / float64(sampleRate*bytesPerSample),
					Duration:      float64(len(window)) / float64(sampleRate*bytesPerSample),
					SampleRate:    sampleRate,
					PCM16Base64:   base64.StdEncoding.EncodeToString(window),
					InitialPrompt: cfg.InitialPrompt,
				}
				if err := worker.send(req); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return err
				}
				nextSubmit += stepBytes
				if len(pcm) > windowBytes {
					pcm = append([]byte(nil), pcm[len(pcm)-windowBytes:]...)
				}
			}
		}

		drained, err := worker.drain()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		for _, resp := range drained {
			pending[resp.Seq] = resp
		}
		for {
			resp, ok := pending[nextPrintSeq]
			if !ok {
				break
			}
			delete(pending, nextPrintSeq)
			windowEnd := resp.Offset + resp.Duration
			if len(resp.Words) == 0 && strings.TrimSpace(resp.Text) != "" {
				fields := strings.Fields(resp.Text)
				step := resp.Duration / float64(len(fields)+1)
				for i, w := range fields {
					end := step * float64(i+1)
					resp.Words = append(resp.Words, whisperWord{Word: w, Start: end - step, End: end})
				}
			}
			if resp.Error != "" {
				if err := wordWriter.clearEphemeral(); err != nil {
					return err
				}
				status(cfg, "whisper", fmt.Sprintf("window %d failed: %s", resp.Seq, resp.Error))
			} else {
				before := printedUntil
				if err := printStableWords(ctx, cfg, wordWriter, resp, windowEnd-holdbackSeconds, &printedUntil); err != nil {
					return err
				}
				if printedUntil > before {
					transcriptStarted = true
				}
			}
			nextPrintSeq++
		}
		if ctx.Err() != nil {
			if err := wordWriter.clearEphemeral(); err != nil {
				return err
			}
			return ctx.Err()
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) || isClosedPipeError(readErr) {
				break
			}
			return readErr
		}
		if transcriptStarted {
			if err := wordWriter.tickSpinner(); err != nil {
				return err
			}
		} else if err := wordWriter.tickTuning(); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			if err := wordWriter.clearEphemeral(); err != nil {
				return err
			}
			return ctx.Err()
		case err := <-ffmpegDone:
			if ctx.Err() != nil {
				if clearErr := wordWriter.clearEphemeral(); clearErr != nil {
					return clearErr
				}
				return ctx.Err()
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return ctx.Err()
		default:
		}
	}
	return ctx.Err()
}

type liveWhisperWorker struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	results chan whisperResponse
	errors  chan error
}

type audioMonitor struct {
	enabled bool
	cmd     *exec.Cmd
	chunks  chan []byte
	errors  chan error
	done    chan error
}

func startPCMStream(ctx context.Context, cfg Config, streamURL string) (io.Reader, <-chan error, error) {
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", streamURL,
		"-vn", "-ac", "1", "-ar", "16000",
		"-f", "s16le", "-c:a", "pcm_s16le",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, cfg.FFmpegBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = prefixedStderr(cfg, "ffmpeg")
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		done <- err
	}()
	return stdout, done, nil
}

func startAudioMonitor(ctx context.Context, cfg Config) (*audioMonitor, error) {
	if !cfg.MonitorAudio {
		return &audioMonitor{}, nil
	}
	if err := requireBinaries(cfg.FFplayBin); err != nil {
		return nil, err
	}
	cmd := audioMonitorCommand(ctx, cfg)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = prefixedStderr(cfg, "ffplay")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	monitor := &audioMonitor{
		enabled: true,
		cmd:     cmd,
		chunks:  make(chan []byte, 64),
		errors:  make(chan error, 1),
		done:    make(chan error, 1),
	}
	go monitor.writeToPlayer(stdin)
	go func() {
		err := cmd.Wait()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		monitor.done <- err
	}()
	status(cfg, "audio", "monitoring stream through system audio output")
	return monitor, nil
}

func audioMonitorCommand(ctx context.Context, cfg Config) *exec.Cmd {
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nodisp",
		"-f", "s16le", "-sample_rate", "16000", "-ch_layout", "mono",
		"-i", "pipe:0",
	}
	return exec.CommandContext(ctx, cfg.FFplayBin, args...)
}

func (m *audioMonitor) writeToPlayer(stdin io.WriteCloser) {
	defer stdin.Close()
	for chunk := range m.chunks {
		if _, err := stdin.Write(chunk); err != nil {
			if !isClosedPipeError(err) {
				m.errors <- err
			}
			return
		}
	}
}

func (m *audioMonitor) write(p []byte) error {
	if m == nil || !m.enabled {
		return nil
	}
	select {
	case err := <-m.errors:
		if isClosedPipeError(err) {
			return nil
		}
		return err
	default:
	}
	chunk := append([]byte(nil), p...)
	select {
	case m.chunks <- chunk:
	default:
		// Playback is only a monitor path; keep transcription moving if the audio device lags.
	}
	return nil
}

func (m *audioMonitor) close() {
	if m == nil || !m.enabled {
		return
	}
	close(m.chunks)
	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		_ = m.cmd.Process.Kill()
		<-m.done
	}
}

func startWhisperWorker(cfg Config) (*liveWhisperWorker, error) {
	script := liveWhisperWorkerScript
	model := cfg.Model
	if cfg.Backend == backendMLX {
		script = liveMLXWorkerScript
		model = mlxModelRef(cfg.Model)
	}
	args := []string{"-u", "-c", script, model, filepath.Join(cfg.WorkDir, "models"), cfg.Language}
	cmd := exec.Command(cfg.PythonBin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = prefixedStderr(cfg, "whisper")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	worker := &liveWhisperWorker{
		cmd:     cmd,
		stdin:   stdin,
		results: make(chan whisperResponse, 32),
		errors:  make(chan error, 1),
	}
	go worker.read(stdout)
	return worker, nil
}

func (w *liveWhisperWorker) send(req whisperRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.stdin.Write(data)
	return err
}

func (w *liveWhisperWorker) drain() ([]whisperResponse, error) {
	var out []whisperResponse
	for {
		select {
		case resp := <-w.results:
			out = append(out, resp)
		case err := <-w.errors:
			if isClosedPipeError(err) {
				return out, nil
			}
			return out, err
		default:
			return out, nil
		}
	}
}

func (w *liveWhisperWorker) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var resp whisperResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			w.errors <- err
			return
		}
		w.results <- resp
	}
	if err := scanner.Err(); err != nil {
		if isClosedPipeError(err) {
			return
		}
		w.errors <- err
	}
}

func isClosedPipeError(err error) bool {
	return err != nil && (errors.Is(err, os.ErrClosed) || strings.Contains(err.Error(), "file already closed"))
}

func (w *liveWhisperWorker) close() {
	_ = w.stdin.Close()
	done := make(chan error, 1)
	go func() {
		done <- w.cmd.Wait()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = w.cmd.Process.Kill()
		<-done
	}
}

func transcribe(ctx context.Context, cfg Config, model, audioPath string) (whisperOutput, error) {
	if cfg.Backend == backendMLX {
		return transcribeMLX(ctx, cfg, model, audioPath)
	}
	outDir := filepath.Join(cfg.WorkDir, "transcripts", model)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return whisperOutput{}, err
	}
	args := []string{
		audioPath,
		"--model", model,
		"--model_dir", filepath.Join(cfg.WorkDir, "models"),
		"--language", cfg.Language,
		"--task", "transcribe",
		"--output_format", "json",
		"--output_dir", outDir,
		"--verbose", "False",
		"--word_timestamps", "True",
		"--fp16", "False",
		"--condition_on_previous_text", "False",
	}
	if cfg.InitialPrompt != "" {
		args = append(args, "--initial_prompt", cfg.InitialPrompt)
	}
	cmd := exec.CommandContext(ctx, cfg.WhisperBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return whisperOutput{}, ctx.Err()
		}
		return whisperOutput{}, fmt.Errorf("whisper failed for %s: %w: %s", filepath.Base(audioPath), err, strings.TrimSpace(stderr.String()))
	}
	jsonPath := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))+".json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return whisperOutput{}, err
	}
	var out whisperOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return whisperOutput{}, err
	}
	return out, nil
}

func transcribeMLX(ctx context.Context, cfg Config, model, audioPath string) (whisperOutput, error) {
	outDir := filepath.Join(cfg.WorkDir, "transcripts", "mlx-"+model)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return whisperOutput{}, err
	}
	args := []string{
		"-u", "-c", mlxTranscribeScript,
		audioPath,
		mlxModelRef(model),
		cfg.Language,
		cfg.InitialPrompt,
	}
	cmd := exec.CommandContext(ctx, cfg.PythonBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return whisperOutput{}, ctx.Err()
		}
		return whisperOutput{}, fmt.Errorf("mlx-whisper failed for %s: %w: %s", filepath.Base(audioPath), err, strings.TrimSpace(stderr.String()))
	}
	jsonData, err := mlxJSONFromStdout(stdout.Bytes())
	if err != nil {
		return whisperOutput{}, err
	}
	var out whisperOutput
	if err := json.Unmarshal(jsonData, &out); err != nil {
		return whisperOutput{}, err
	}
	jsonPath := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))+".json")
	if err := os.WriteFile(jsonPath, jsonData, 0o644); err != nil {
		return whisperOutput{}, err
	}
	return out, nil
}

func runPreview(ctx context.Context, cfg Config) error {
	fixture, transcript, err := loadFixture(cfg)
	if err != nil {
		return err
	}
	if cfg.PreviewOut == "" {
		cfg.PreviewOut = fixture.AudioPath
	}
	if err := requireBinaries(cfg.SagBin); err != nil {
		return err
	}
	if os.Getenv("ELEVENLABS_API_KEY") == "" && os.Getenv("ELEVENLABS_API_KEY_FILE") == "" {
		return fmt.Errorf("set ELEVENLABS_API_KEY or ELEVENLABS_API_KEY_FILE before running preview")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.PreviewOut), 0o755); err != nil {
		return err
	}
	args := []string{
		"speak",
		"--no-play",
		"--no-stream",
		"--model-id", "eleven_multilingual_v2",
		"--style", "0.18",
		"--stability", "0.62",
		"--similarity", "0.78",
		"--speaker-boost",
		"--output", cfg.PreviewOut,
		transcript,
	}
	if cfg.SagVoice != "" {
		args = append(args[:1], append([]string{"--voice", cfg.SagVoice}, args[1:]...)...)
	}
	cmd := exec.CommandContext(ctx, cfg.SagBin, args...)
	cmd.Stdout = io.Discard
	if cfg.Verbose {
		cmd.Stdout = os.Stderr
	}
	cmd.Stderr = prefixedStderr(cfg, "sag")
	status(cfg, "preview", "generating "+cfg.PreviewOut)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	status(cfg, "preview", "wrote "+cfg.PreviewOut)
	return nil
}

func runBenchmark(ctx context.Context, cfg Config) error {
	if err := validateBackend(cfg.Backend); err != nil {
		return err
	}
	if err := validateBenchmarkConfig(cfg); err != nil {
		return err
	}
	fixture, transcript, err := loadFixture(cfg)
	if err != nil {
		return err
	}
	if cfg.PreviewOut == "" {
		cfg.PreviewOut = fixture.AudioPath
	}
	backend, err := prepareBackend(ctx, &cfg)
	if err != nil {
		return err
	}
	audio := cfg.PreviewOut
	if _, err := os.Stat(audio); err != nil {
		return fmt.Errorf("%s not found; run `go run ./cmd/tr1-lab preview --fixture %s` first", audio, fixture.Name)
	}
	if err := ensureDirs(cfg); err != nil {
		return err
	}

	models := splitCSV(cfg.Models)
	if len(models) == 0 {
		return fmt.Errorf("no models supplied")
	}
	audioSeconds := audioDuration(ctx, audio)

	results := make([]benchResult, 0, len(models))
	for _, model := range models {
		status(cfg, "benchmark", "running "+backend+" model "+model)
		start := time.Now()
		out, err := transcribeForBenchmark(ctx, cfg, model, audio)
		if err != nil {
			return err
		}
		duration := time.Since(start)
		text := normalizeText(out.Text)
		ref := normalizeText(transcript)
		result := benchResult{
			Backend:  backend,
			Model:    model,
			WER:      wordErrorRate(strings.Fields(ref), strings.Fields(text)),
			Words:    len(strings.Fields(text)),
			Duration: duration,
			RTF:      realTimeFactor(duration, audioSeconds),
			Text:     strings.TrimSpace(out.Text),
		}
		results = append(results, result)
	}

	return writeBenchmarkResults(os.Stdout, results)
}

func validateBenchmarkConfig(cfg Config) error {
	switch strings.ToLower(strings.TrimSpace(cfg.BenchmarkMode)) {
	case benchmarkModeWhole, benchmarkModeChunked:
	default:
		return fmt.Errorf("--mode must be one of: whole, chunked")
	}
	if strings.ToLower(strings.TrimSpace(cfg.BenchmarkMode)) == benchmarkModeChunked {
		return validateStreamConfig(cfg)
	}
	return nil
}

func transcribeForBenchmark(ctx context.Context, cfg Config, model, audioPath string) (whisperOutput, error) {
	if strings.ToLower(strings.TrimSpace(cfg.BenchmarkMode)) == benchmarkModeChunked {
		chunkedCfg := cfg
		chunkedCfg.Model = model
		chunkedCfg.WordDelay = 0
		text, err := transcribeChunkedFile(ctx, chunkedCfg, audioPath)
		if err != nil {
			return whisperOutput{}, err
		}
		return whisperOutput{Text: text}, nil
	}
	return transcribe(ctx, cfg, model, audioPath)
}

func transcribeChunkedFile(ctx context.Context, cfg Config, audioPath string) (string, error) {
	ffmpegStdout, ffmpegDone, err := startPCMStream(ctx, cfg, audioPath)
	if err != nil {
		return "", err
	}
	worker, err := startWhisperWorker(cfg)
	if err != nil {
		return "", err
	}
	defer worker.close()

	const sampleRate = 16000
	const bytesPerSample = 2
	windowBytes := cfg.ChunkSeconds * sampleRate * bytesPerSample
	stepBytes := cfg.StepSeconds * sampleRate * bytesPerSample
	holdbackSeconds := cfg.Holdback.Seconds()

	var pcm []byte
	readBuf := make([]byte, 4096)
	nextSubmit := stepBytes
	seq := 0
	totalBytes := 0
	printedUntil := 0.0
	var words []string
	pending := map[int]whisperResponse{}
	nextPrintSeq := 1

	submit := func() error {
		seq++
		window := pcm
		if len(window) > windowBytes {
			window = window[len(window)-windowBytes:]
		}
		offsetBytes := totalBytes - len(window)
		req := whisperRequest{
			Seq:           seq,
			Offset:        float64(offsetBytes) / float64(sampleRate*bytesPerSample),
			Duration:      float64(len(window)) / float64(sampleRate*bytesPerSample),
			SampleRate:    sampleRate,
			PCM16Base64:   base64.StdEncoding.EncodeToString(window),
			InitialPrompt: cfg.InitialPrompt,
		}
		if err := worker.send(req); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if len(pcm) > windowBytes {
			pcm = append([]byte(nil), pcm[len(pcm)-windowBytes:]...)
		}
		return nil
	}

	flushReady := func() error {
		drained, err := worker.drain()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		for _, resp := range drained {
			pending[resp.Seq] = resp
		}
		for {
			resp, ok := pending[nextPrintSeq]
			if !ok {
				break
			}
			delete(pending, nextPrintSeq)
			windowEnd := resp.Offset + resp.Duration
			if len(resp.Words) == 0 && strings.TrimSpace(resp.Text) != "" {
				fields := strings.Fields(resp.Text)
				step := resp.Duration / float64(len(fields)+1)
				for i, w := range fields {
					end := step * float64(i+1)
					resp.Words = append(resp.Words, whisperWord{Word: w, Start: end - step, End: end})
				}
			}
			if resp.Error != "" {
				return fmt.Errorf("window %d failed: %s", resp.Seq, resp.Error)
			}
			appendStableWords(resp, windowEnd-holdbackSeconds, &printedUntil, &words)
			nextPrintSeq++
		}
		return nil
	}

	for {
		n, readErr := ffmpegStdout.Read(readBuf)
		if n > 0 {
			pcm = append(pcm, readBuf[:n]...)
			totalBytes += n
			for totalBytes >= nextSubmit && len(pcm) >= minInt(windowBytes, totalBytes) {
				if err := submit(); err != nil {
					return "", err
				}
				nextSubmit += stepBytes
			}
		}
		if err := flushReady(); err != nil {
			return "", err
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) || isClosedPipeError(readErr) {
				break
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", readErr
		}
	}
	if totalBytes > 0 && (seq == 0 || totalBytes > nextSubmit-stepBytes) {
		if err := submit(); err != nil {
			return "", err
		}
	}
	_ = worker.stdin.Close()
	for nextPrintSeq <= seq {
		if err := flushReady(); err != nil {
			return "", err
		}
		if nextPrintSeq > seq {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case err := <-ffmpegDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				return "", err
			}
		case <-time.After(25 * time.Millisecond):
		}
	}
	select {
	case err := <-ffmpegDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			return "", err
		}
	default:
	}
	return strings.Join(words, " "), nil
}

func loadFixture(cfg Config) (benchmarkFixture, string, error) {
	fixture, err := lookupFixture(cfg.Fixture)
	if err != nil {
		return benchmarkFixture{}, "", err
	}
	data, err := os.ReadFile(fixture.TranscriptPath)
	if err != nil {
		return benchmarkFixture{}, "", err
	}
	transcript := strings.TrimSpace(string(data))
	if transcript == "" {
		return benchmarkFixture{}, "", fmt.Errorf("%s is empty", fixture.TranscriptPath)
	}
	return fixture, transcript, nil
}

func lookupFixture(query string) (benchmarkFixture, error) {
	key := stationKey(query)
	if key == "" {
		key = stationKey(defaultFixtureName)
	}
	for _, fixture := range benchmarkFixtures {
		if stationKey(fixture.Name) == key {
			return fixture, nil
		}
	}
	return benchmarkFixture{}, fmt.Errorf("unknown fixture %q (try one of: %s)", query, fixtureHelp())
}

func fixtureHelp() string {
	names := make([]string, 0, len(benchmarkFixtures))
	for _, fixture := range benchmarkFixtures {
		names = append(names, fixture.Name)
	}
	return strings.Join(names, ", ")
}

func applySelectedStationDefaults(cfg *Config) (string, string, error) {
	if cfg.StreamURL != "" {
		return "custom stream", cfg.StreamURL, nil
	}
	selected, err := lookupStation(cfg.Station)
	if err != nil {
		return "", "", err
	}
	if !cfg.LanguageSet && selected.Language != "" {
		cfg.Language = selected.Language
	}
	return selected.Name, selected.URL, nil
}

func streamSelection(cfg Config) (string, string, error) {
	if cfg.StreamURL != "" {
		return "custom stream", cfg.StreamURL, nil
	}
	selected, err := lookupStation(cfg.Station)
	if err != nil {
		return "", "", err
	}
	return selected.Name, selected.URL, nil
}

func lookupStation(query string) (Station, error) {
	key := stationKey(query)
	if key == "" {
		key = stationKey(defaultStationAlias)
	}
	for _, s := range stations {
		if stationKey(s.Name) == key {
			return s, nil
		}
		for _, alias := range s.Aliases {
			if stationKey(alias) == key {
				return s, nil
			}
		}
	}
	return Station{}, fmt.Errorf("unknown station %q (try one of: %s)", query, stationHelp())
}

func stationHelp() string {
	names := make([]string, 0, len(stations))
	for _, s := range stations {
		names = append(names, s.Aliases[0])
	}
	return strings.Join(names, ", ")
}

func stationKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		"ą", "a",
		"ć", "c",
		"ę", "e",
		"ł", "l",
		"ń", "n",
		"ó", "o",
		"ś", "s",
		"ż", "z",
		"ź", "z",
	)
	s = replacer.Replace(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func stationSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		"ą", "a",
		"ć", "c",
		"ę", "e",
		"ł", "l",
		"ń", "n",
		"ó", "o",
		"ś", "s",
		"ż", "z",
		"ź", "z",
	)
	s = replacer.Replace(s)
	var b strings.Builder
	sep := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			if sep && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			sep = false
		default:
			sep = true
		}
	}
	out := b.String()
	if out == "" {
		return "custom-stream"
	}
	return out
}

func recordingStationSlug(cfg Config, selectedName string) string {
	if cfg.StreamURL == "" {
		if selected, err := lookupStation(cfg.Station); err == nil && len(selected.Aliases) > 0 {
			return stationSlug(selected.Aliases[0])
		}
	}
	return stationSlug(selectedName)
}

func recordingCacheRoot(cfg Config) (string, error) {
	if cfg.CacheDir != "" {
		return cfg.CacheDir, nil
	}
	if cacheDir := os.Getenv("TR1_CACHE_DIR"); cacheDir != "" {
		return cacheDir, nil
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "tr1"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "tr1"), nil
}

func recordingStationRoot(cacheRoot, stationSlug string) string {
	return filepath.Join(cacheRoot, "recordings", stationSlug)
}

func recordingSegmentPattern(cacheRoot, stationSlug string) string {
	return filepath.Join(recordingStationRoot(cacheRoot, stationSlug), "%Y", "%m", "%d", stationSlug+"_%Y%m%dT%H%M%SZ"+defaultRecordExt)
}

func recordingTranscriptPath(cacheRoot string, rec recording, backend string, cfg Config) string {
	start := rec.StartUTC.UTC()
	year, month, day := "unknown", "unknown", "unknown"
	if !start.IsZero() {
		year = start.Format("2006")
		month = start.Format("01")
		day = start.Format("02")
	}
	recordingName := strings.TrimSuffix(filepath.Base(rec.Path), filepath.Ext(rec.Path))
	if recordingName == "" {
		recordingName = "recording"
	}
	return filepath.Join(
		cacheRoot,
		"transcripts",
		transcriptCacheV1,
		stationSlug(rec.Station),
		year,
		month,
		day,
		stationSlug(recordingName),
		stationSlug(backend),
		stationSlug(cfg.Model),
		stationSlug(cfg.Language)+".json",
	)
}

func listRecordings(cacheRoot, stationQuery string) ([]recording, error) {
	root := filepath.Join(cacheRoot, "recordings")
	var stationFilter string
	if strings.TrimSpace(stationQuery) != "" {
		selected, err := lookupStation(stationQuery)
		if err != nil {
			return nil, err
		}
		stationFilter = stationSlug(selected.Aliases[0])
	}

	var out []recording
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if stationFilter != "" && path == root {
				return nil
			}
			if stationFilter != "" {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				if rel != "." {
					parts := strings.Split(rel, string(filepath.Separator))
					if len(parts) > 0 && parts[0] != stationFilter {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		rec, ok, err := recordingFromPath(root, path)
		if err != nil || !ok {
			return err
		}
		if stationFilter != "" && rec.Station != stationFilter {
			return nil
		}
		out = append(out, rec)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Station != out[j].Station {
			return out[i].Station < out[j].Station
		}
		return out[i].StartUTC.Before(out[j].StartUTC)
	})
	return out, nil
}

func stationFromRecordingFilename(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if station, _, ok := strings.Cut(base, "_"); ok && station != "" {
		return stationSlug(station)
	}
	return "external"
}

func recordingStartFromFilename(path string) time.Time {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if _, timestamp, ok := strings.Cut(base, "_"); ok {
		if start, err := time.Parse("20060102T150405Z", timestamp); err == nil {
			return start
		}
	}
	return time.Time{}
}

func recordingFromPath(root, path string) (recording, bool, error) {
	if filepath.Ext(path) != defaultRecordExt {
		return recording{}, false, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return recording{}, false, err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 5 {
		return recording{}, false, nil
	}
	station := parts[0]
	base := filepath.Base(path)
	prefix := station + "_"
	if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, defaultRecordExt) {
		return recording{}, false, nil
	}
	timestamp := strings.TrimSuffix(strings.TrimPrefix(base, prefix), defaultRecordExt)
	start, err := time.Parse("20060102T150405Z", timestamp)
	if err != nil {
		return recording{}, false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return recording{}, false, err
	}
	if info.Size() == 0 {
		return recording{}, false, nil
	}
	return recording{
		Station:  station,
		StartUTC: start,
		Path:     path,
		Bytes:    info.Size(),
	}, true, nil
}

func writeRecordings(w io.Writer, recordings []recording) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "START_UTC\tSTATION\tBYTES\tPATH"); err != nil {
		return err
	}
	for _, rec := range recordings {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", rec.StartUTC.Format(time.RFC3339), rec.Station, rec.Bytes, rec.Path); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func resolveStreamURL(ctx context.Context, rawURL string) (string, error) {
	if !strings.Contains(strings.ToLower(rawURL), ".pls") && !strings.Contains(strings.ToLower(rawURL), "stream.pls") {
		return rawURL, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tr1/0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("playlist request failed: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(strings.ToLower(line), "file") {
			if _, value, ok := strings.Cut(line, "="); ok && strings.HasPrefix(value, "http") {
				return strings.TrimSpace(value), nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return rawURL, nil
}

func ensureDirs(cfg Config) error {
	for _, dir := range []string{
		cfg.WorkDir,
		filepath.Join(cfg.WorkDir, "models"),
		filepath.Join(cfg.WorkDir, "transcripts"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func requireBinaries(names ...string) error {
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("%s not found in PATH", name)
		}
	}
	return nil
}

func validateBackend(backend string) error {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case backendAuto, backendCPU, backendMLX:
		return nil
	default:
		return fmt.Errorf("--backend must be one of: auto, cpu, mlx")
	}
}

func prepareBackend(ctx context.Context, cfg *Config) (string, error) {
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == backendAuto {
		if mlxSupportedHardware() {
			if _, err := exec.LookPath(cfg.UVBin); err == nil {
				backend = backendMLX
			} else {
				backend = backendCPU
			}
		} else {
			backend = backendCPU
		}
	}
	cfg.Backend = backend
	switch backend {
	case backendCPU:
		if err := requireBinaries(cfg.FFmpegBin, cfg.WhisperBin); err != nil {
			return "", err
		}
		pythonBin, err := whisperPythonBin(*cfg)
		if err != nil {
			return "", err
		}
		cfg.PythonBin = pythonBin
		return backendCPU, nil
	case backendMLX:
		if err := requireBinaries(cfg.FFmpegBin, cfg.UVBin); err != nil {
			return "", err
		}
		pythonBin, err := ensureMLXEnv(ctx, *cfg)
		if err != nil {
			return "", err
		}
		cfg.PythonBin = pythonBin
		return backendMLX, nil
	default:
		return "", fmt.Errorf("--backend must be one of: auto, cpu, mlx")
	}
}

func mlxSupportedHardware() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

func ensureMLXEnv(ctx context.Context, cfg Config) (string, error) {
	projectDir := filepath.Join(cfg.WorkDir, "mlx")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return "", err
	}
	pyprojectPath := filepath.Join(projectDir, "pyproject.toml")
	if err := os.WriteFile(pyprojectPath, []byte(embeddedPyproject), 0o644); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, cfg.UVBin, "sync", "--project", projectDir, "--quiet")
	cmd.Stderr = prefixedStderr(cfg, "uv")
	if cfg.Verbose {
		cmd.Stdout = os.Stderr
	} else {
		cmd.Stdout = io.Discard
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("uv sync for MLX runtime failed: %w", err)
	}
	return filepath.Join(projectDir, ".venv", "bin", "python"), nil
}

func mlxModelRef(model string) string {
	if strings.Contains(model, "/") || strings.Contains(model, string(filepath.Separator)) {
		return model
	}
	return fmt.Sprintf(defaultMLXModelRepo, model)
}

func whisperPythonBin(cfg Config) (string, error) {
	if cfg.PythonBin != "" {
		if _, err := exec.LookPath(cfg.PythonBin); err != nil {
			return "", fmt.Errorf("%s not found in PATH", cfg.PythonBin)
		}
		return cfg.PythonBin, nil
	}
	whisperPath, err := exec.LookPath(cfg.WhisperBin)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH", cfg.WhisperBin)
	}
	file, err := os.Open(whisperPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "#!") && strings.Contains(filepath.Base(line), "python") {
		python := strings.TrimPrefix(line, "#!")
		if _, err := os.Stat(python); err == nil {
			return python, nil
		}
	}
	return "", fmt.Errorf("could not infer Python interpreter from %s; pass --python-bin or set TR1_PYTHON_BIN", whisperPath)
}

func wordErrorRate(ref, hyp []string) float64 {
	if len(ref) == 0 {
		if len(hyp) == 0 {
			return 0
		}
		return 1
	}
	prev := make([]int, len(hyp)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ref); i++ {
		cur := make([]int, len(hyp)+1)
		cur[0] = i
		for j := 1; j <= len(hyp); j++ {
			cost := 0
			if ref[i-1] != hyp[j-1] {
				cost = 1
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return float64(prev[len(hyp)]) / float64(len(ref))
}

func normalizeText(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func audioDuration(ctx context.Context, audioPath string) float64 {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return 0
	}
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", audioPath)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return 0
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(stdout.String()), 64)
	if err != nil {
		return 0
	}
	return seconds
}

func mlxJSONFromStdout(stdout []byte) ([]byte, error) {
	const marker = "TR1_JSON:"
	if i := bytes.LastIndex(stdout, []byte(marker)); i >= 0 {
		line := stdout[i+len(marker):]
		if j := bytes.IndexByte(line, '\n'); j >= 0 {
			line = line[:j]
		}
		return bytes.TrimSpace(line), nil
	}
	if i := bytes.LastIndexByte(stdout, '{'); i >= 0 {
		return bytes.TrimSpace(stdout[i:]), nil
	}
	return nil, fmt.Errorf("mlx-whisper did not print JSON output")
}

func realTimeFactor(duration time.Duration, audioSeconds float64) float64 {
	if audioSeconds <= 0 {
		return 0
	}
	return duration.Seconds() / audioSeconds
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envHasValue(name string) bool {
	value, ok := os.LookupEnv(name)
	return ok && value != ""
}

func intFromEnv(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func durationFromEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return d
}

func boolFromEnv(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

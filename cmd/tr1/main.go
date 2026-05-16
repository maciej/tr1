package main

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
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode"
	"unsafe"

	"github.com/spf13/cobra"
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
)

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

type station struct {
	Name    string
	URL     string
	Aliases []string
}

var stations = []station{
	{
		Name:    "TokFM",
		URL:     "http://www.tuba.fm/stream.pls?radio=10&mp3=1",
		Aliases: []string{"tokfm", "tok", "tok-fm"},
	},
	{
		Name:    "Polskie Radio Jedynka",
		URL:     "http://stream3.polskieradio.pl:8900/listen.pls",
		Aliases: []string{"jedynka", "pr1", "program1", "program-1", "radio1", "radio-1", "one", "1"},
	},
	{
		Name:    "Program Drugi Polskiego Radia",
		URL:     "http://stream3.polskieradio.pl:8902/listen.pls",
		Aliases: []string{"dwojka", "dwójka", "pr2", "program2", "program-2", "drugi", "radio2", "radio-2", "two", "2"},
	},
	{
		Name:    "Trójka",
		URL:     "http://stream3.polskieradio.pl:8904/listen.pls",
		Aliases: []string{"trojka", "trójka", "pr3", "program3", "program-3", "troika", "radio3", "radio-3", "three", "3"},
	},
	{
		Name:    "RMF FM",
		URL:     "https://rs201-krk-cyfronet.rmfstream.pl/rmf_fm",
		Aliases: []string{"rmf", "rmffm", "rmf-fm"},
	},
	{
		Name:    "Radio ZET",
		URL:     "https://playerservices.streamtheworld.com/api/livestream-redirect/RADIO_ZET_SC",
		Aliases: []string{"zet", "radiozet", "radio-zet"},
	},
}

//go:embed mlx.pyproject.toml
var embeddedPyproject string

type config struct {
	station       string
	streamURL     string
	model         string
	models        string
	language      string
	backend       string
	chunkSeconds  int
	stepSeconds   int
	holdback      time.Duration
	workDir       string
	whisperBin    string
	uvBin         string
	pythonBin     string
	ffmpegBin     string
	ffplayBin     string
	sagBin        string
	sagVoice      string
	fixture       string
	wordDelay     time.Duration
	previewOut    string
	benchmarkMode string
	initialPrompt string
	monitorAudio  bool
	verbose       bool
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

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCommand(ctx).Execute(); err != nil && !errors.Is(err, context.Canceled) {
		fatal(err)
	}
}

func defaultConfig() config {
	return config{
		station:       getenv("TR1_STATION", defaultStationAlias),
		streamURL:     getenv("TR1_STREAM_URL", ""),
		model:         getenv("TR1_MODEL", "medium"),
		models:        getenv("TR1_MODELS", "tiny,base"),
		language:      getenv("TR1_LANGUAGE", "Polish"),
		backend:       getenv("TR1_BACKEND", backendAuto),
		chunkSeconds:  intFromEnv("TR1_CHUNK_SECONDS", 12),
		stepSeconds:   intFromEnv("TR1_STEP_SECONDS", 3),
		holdback:      durationFromEnv("TR1_HOLDBACK", 1500*time.Millisecond),
		workDir:       getenv("TR1_WORKDIR", defaultWorkDir),
		whisperBin:    getenv("TR1_WHISPER_BIN", "whisper"),
		uvBin:         getenv("TR1_UV_BIN", "uv"),
		pythonBin:     getenv("TR1_PYTHON_BIN", ""),
		ffmpegBin:     getenv("TR1_FFMPEG_BIN", "ffmpeg"),
		ffplayBin:     getenv("TR1_FFPLAY_BIN", "ffplay"),
		sagBin:        getenv("TR1_SAG_BIN", "sag"),
		sagVoice:      getenv("TR1_SAG_VOICE", ""),
		fixture:       getenv("TR1_FIXTURE", defaultFixtureName),
		wordDelay:     durationFromEnv("TR1_WORD_DELAY", 35*time.Millisecond),
		previewOut:    "",
		benchmarkMode: getenv("TR1_BENCHMARK_MODE", benchmarkModeWhole),
		initialPrompt: "Polski serwis informacyjny radiowy. Poprawna polska interpunkcja i nazwy własne.",
		monitorAudio:  boolFromEnv("TR1_PLAY", false),
		verbose:       boolFromEnv("TR1_VERBOSE", false),
	}
}

func newRootCommand(ctx context.Context) *cobra.Command {
	cfg := defaultConfig()

	rootCmd := &cobra.Command{
		Use:           "tr1 [station]",
		Short:         "Terminal radio receiver and Whisper transcription loop",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyStationArg(&cfg, args); err != nil {
				return err
			}
			if err := validateStreamConfig(cfg); err != nil {
				return err
			}
			return runStream(ctx, cfg)
		},
	}

	rootCmd.PersistentFlags().BoolVar(&cfg.verbose, "verbose", cfg.verbose, "print diagnostic status messages to stderr")
	addStreamFlags(rootCmd, &cfg)

	command := func(validate func(config) error, run func(context.Context, config) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			if validate == nil {
				return run(ctx, cfg)
			}
			if err := validate(cfg); err != nil {
				return err
			}
			return run(ctx, cfg)
		}
	}

	streamCmd := &cobra.Command{
		Use:   "stream [station]",
		Short: "Stream radio audio and print live Whisper transcription",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyStationArg(&cfg, args); err != nil {
				return err
			}
			return command(validateStreamConfig, runStream)(cmd, nil)
		},
	}
	addStreamFlags(streamCmd, &cfg)

	stationCmds := stationAliasCommands(&cfg, command(validateStreamConfig, runStream))

	previewCmd := &cobra.Command{
		Use:   "preview",
		Short: "Generate synthetic news-broadcast preview audio with sag",
		Args:  cobra.NoArgs,
		RunE:  command(nil, runPreview),
	}
	addPreviewFlags(previewCmd, &cfg)

	localPreviewCmd := &cobra.Command{
		Use:   "preview-local",
		Short: "Generate local benchmark preview audio with macOS speech synthesis",
		Args:  cobra.NoArgs,
		RunE:  command(nil, runLocalPreview),
	}
	addLocalPreviewFlags(localPreviewCmd, &cfg)

	benchmarkCmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Benchmark Whisper models against the generated preview audio",
		Args:  cobra.NoArgs,
		RunE:  command(nil, runBenchmark),
	}
	addBenchmarkFlags(benchmarkCmd, &cfg)

	stationsCmd := &cobra.Command{
		Use:   "stations",
		Short: "List available radio stations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeStations(cmd.OutOrStdout())
		},
	}

	rootCmd.AddCommand(
		streamCmd,
		previewCmd,
		localPreviewCmd,
		benchmarkCmd,
		stationsCmd,
	)
	rootCmd.AddCommand(stationCmds...)

	return rootCmd
}

func writeStations(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ALIAS\tNAME\tURL\tALIASES"); err != nil {
		return err
	}
	for _, s := range stations {
		primaryAlias := ""
		if len(s.Aliases) > 0 {
			primaryAlias = s.Aliases[0]
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", primaryAlias, s.Name, s.URL, strings.Join(s.Aliases, ", ")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func stationAliasCommands(cfg *config, run func(*cobra.Command, []string) error) []*cobra.Command {
	seen := map[string]bool{}
	var out []*cobra.Command
	for _, s := range stations {
		for _, alias := range s.Aliases {
			if alias == "" || seen[alias] {
				continue
			}
			seen[alias] = true
			alias := alias
			cmd := &cobra.Command{
				Use:    alias,
				Hidden: true,
				Args:   cobra.NoArgs,
				RunE: func(cmd *cobra.Command, args []string) error {
					if !cmd.Flags().Changed("station") {
						cfg.station = alias
					}
					return run(cmd, args)
				},
			}
			addStreamFlags(cmd, cfg)
			out = append(out, cmd)
		}
	}
	return out
}

func addStreamFlags(cmd *cobra.Command, cfg *config) {
	flags := cmd.Flags()
	flags.StringVarP(&cfg.station, "station", "s", cfg.station, "station alias or canonical name ("+stationHelp()+")")
	flags.StringVar(&cfg.streamURL, "stream-url", cfg.streamURL, "radio stream or playlist URL; overrides --station")
	flags.StringVar(&cfg.model, "model", cfg.model, "Whisper model")
	flags.StringVar(&cfg.language, "language", cfg.language, "Whisper language")
	flags.StringVar(&cfg.backend, "backend", cfg.backend, "transcription backend: auto, cpu, or mlx")
	flags.IntVar(&cfg.chunkSeconds, "window", cfg.chunkSeconds, "rolling transcription window in seconds")
	flags.IntVar(&cfg.stepSeconds, "step", cfg.stepSeconds, "seconds of new audio between Whisper requests")
	flags.StringVar(&cfg.workDir, "workdir", cfg.workDir, "runtime working directory")
	flags.StringVar(&cfg.whisperBin, "whisper-bin", cfg.whisperBin, "whisper executable")
	flags.StringVar(&cfg.uvBin, "uv-bin", cfg.uvBin, "uv executable used to provision the MLX Python runtime")
	flags.StringVar(&cfg.pythonBin, "python-bin", cfg.pythonBin, "python executable for live Whisper worker; defaults to the whisper CLI interpreter")
	flags.StringVar(&cfg.ffmpegBin, "ffmpeg-bin", cfg.ffmpegBin, "ffmpeg executable")
	flags.StringVar(&cfg.ffplayBin, "ffplay-bin", cfg.ffplayBin, "ffplay executable used by --play")
	flags.BoolVar(&cfg.monitorAudio, "play", cfg.monitorAudio, "play live stream audio through the system audio output while transcribing")
	flags.DurationVar(&cfg.wordDelay, "word-delay", cfg.wordDelay, "delay between printed words")
	flags.DurationVar(&cfg.holdback, "holdback", cfg.holdback, "hold back live words near the unstable end of each window")
	flags.StringVar(&cfg.initialPrompt, "initial-prompt", cfg.initialPrompt, "Whisper initial prompt")
}

func addPreviewFlags(cmd *cobra.Command, cfg *config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.fixture, "fixture", cfg.fixture, "benchmark fixture name ("+fixtureHelp()+")")
	flags.StringVar(&cfg.previewOut, "preview-out", cfg.previewOut, "path for ElevenLabs preview audio")
	flags.StringVar(&cfg.sagBin, "sag-bin", cfg.sagBin, "sag executable")
	flags.StringVar(&cfg.sagVoice, "voice", cfg.sagVoice, "sag/ElevenLabs voice name or ID")
}

func addLocalPreviewFlags(cmd *cobra.Command, cfg *config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.fixture, "fixture", cfg.fixture, "benchmark fixture name ("+fixtureHelp()+")")
	flags.StringVar(&cfg.previewOut, "preview-out", cfg.previewOut, "path for local preview audio")
	flags.StringVar(&cfg.ffmpegBin, "ffmpeg-bin", cfg.ffmpegBin, "ffmpeg executable")
}

func addBenchmarkFlags(cmd *cobra.Command, cfg *config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.fixture, "fixture", cfg.fixture, "benchmark fixture name ("+fixtureHelp()+")")
	flags.StringVar(&cfg.models, "models", cfg.models, "comma-separated Whisper models")
	flags.StringVar(&cfg.previewOut, "preview-out", cfg.previewOut, "path for benchmark preview audio")
	flags.StringVar(&cfg.workDir, "workdir", cfg.workDir, "runtime working directory")
	flags.StringVar(&cfg.backend, "backend", cfg.backend, "transcription backend: auto, cpu, or mlx")
	flags.StringVar(&cfg.whisperBin, "whisper-bin", cfg.whisperBin, "whisper executable")
	flags.StringVar(&cfg.uvBin, "uv-bin", cfg.uvBin, "uv executable used to provision the MLX Python runtime")
	flags.StringVar(&cfg.language, "language", cfg.language, "Whisper language")
	flags.StringVar(&cfg.initialPrompt, "initial-prompt", cfg.initialPrompt, "Whisper initial prompt")
	flags.StringVar(&cfg.benchmarkMode, "mode", cfg.benchmarkMode, "benchmark mode: whole or chunked")
	flags.IntVar(&cfg.chunkSeconds, "window", cfg.chunkSeconds, "rolling transcription window in seconds for chunked mode")
	flags.IntVar(&cfg.stepSeconds, "step", cfg.stepSeconds, "seconds of new audio between Whisper requests for chunked mode")
	flags.DurationVar(&cfg.holdback, "holdback", cfg.holdback, "hold back words near the unstable end of each window for chunked mode")
}

func validateStreamConfig(cfg config) error {
	if err := validateBackend(cfg.backend); err != nil {
		return err
	}
	if cfg.chunkSeconds < 3 {
		return fmt.Errorf("--window must be at least 3 seconds")
	}
	if cfg.stepSeconds < 1 {
		return fmt.Errorf("--step must be at least 1 second")
	}
	if cfg.stepSeconds > cfg.chunkSeconds {
		return fmt.Errorf("--step must be less than or equal to --window")
	}
	if cfg.monitorAudio {
		if err := requireBinaries(cfg.ffplayBin); err != nil {
			return err
		}
	}
	return nil
}

func runStream(ctx context.Context, cfg config) error {
	backend, err := prepareBackend(ctx, &cfg)
	if err != nil {
		return err
	}
	if err := ensureDirs(cfg); err != nil {
		return err
	}

	selectedStation, stationURL, err := streamSelection(cfg)
	if err != nil {
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

func streamToWhisper(ctx context.Context, cfg config, streamURL string) error {
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
	windowBytes := cfg.chunkSeconds * sampleRate * bytesPerSample
	stepBytes := cfg.stepSeconds * sampleRate * bytesPerSample
	holdbackSeconds := cfg.holdback.Seconds()

	status(cfg, "ffmpeg", "streaming raw 16 kHz PCM")
	status(cfg, "whisper", fmt.Sprintf("streaming %ds rolling windows every %ds", cfg.chunkSeconds, cfg.stepSeconds))

	wordWriter := newTerminalWordWriter(os.Stdout)
	defer wordWriter.close()

	var pcm []byte
	readBuf := make([]byte, 4096)
	nextSubmit := stepBytes
	seq := 0
	totalBytes := 0
	printedUntil := 0.0
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
					InitialPrompt: cfg.initialPrompt,
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
				status(cfg, "whisper", fmt.Sprintf("window %d failed: %s", resp.Seq, resp.Error))
			} else if err := printStableWords(ctx, cfg, wordWriter, resp, windowEnd-holdbackSeconds, &printedUntil); err != nil {
				return err
			}
			nextPrintSeq++
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) || isClosedPipeError(readErr) {
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return readErr
		}
		select {
		case err := <-ffmpegDone:
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
	stdin   io.WriteCloser
	done    chan error
}

func startPCMStream(ctx context.Context, cfg config, streamURL string) (io.Reader, <-chan error, error) {
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", streamURL,
		"-vn", "-ac", "1", "-ar", "16000",
		"-f", "s16le", "-c:a", "pcm_s16le",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, cfg.ffmpegBin, args...)
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

func startAudioMonitor(ctx context.Context, cfg config) (*audioMonitor, error) {
	if !cfg.monitorAudio {
		return &audioMonitor{}, nil
	}
	if err := requireBinaries(cfg.ffplayBin); err != nil {
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
		stdin:   stdin,
		done:    make(chan error, 1),
	}
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

func audioMonitorCommand(ctx context.Context, cfg config) *exec.Cmd {
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nodisp",
		"-f", "s16le", "-ac", "1", "-ar", "16000",
		"-i", "pipe:0",
	}
	return exec.CommandContext(ctx, cfg.ffplayBin, args...)
}

func (m *audioMonitor) write(p []byte) error {
	if m == nil || !m.enabled {
		return nil
	}
	_, err := m.stdin.Write(p)
	if err != nil && isClosedPipeError(err) {
		return nil
	}
	return err
}

func (m *audioMonitor) close() {
	if m == nil || !m.enabled {
		return
	}
	_ = m.stdin.Close()
	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		_ = m.cmd.Process.Kill()
		<-m.done
	}
}

func startWhisperWorker(cfg config) (*liveWhisperWorker, error) {
	script := liveWhisperWorkerScript()
	model := cfg.model
	if cfg.backend == backendMLX {
		script = liveMLXWorkerScript()
		model = mlxModelRef(cfg.model)
	}
	args := []string{"-u", "-c", script, model, filepath.Join(cfg.workDir, "models"), cfg.language}
	cmd := exec.Command(cfg.pythonBin, args...)
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

func printStableWords(ctx context.Context, cfg config, writer *terminalWordWriter, resp whisperResponse, stableUntil float64, printedUntil *float64) error {
	printed := 0
	for _, word := range resp.Words {
		start := resp.Offset + word.Start
		end := resp.Offset + word.End
		if end <= *printedUntil+0.05 || start < *printedUntil-0.25 || end > stableUntil {
			continue
		}
		text := strings.TrimSpace(word.Word)
		if text == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := writer.writeWord(text, needsSpace(text)); err != nil {
			return err
		}
		*printedUntil = end
		printed++
		if cfg.wordDelay > 0 {
			time.Sleep(cfg.wordDelay)
		}
	}
	if printed > 0 {
		status(cfg, "whisper", fmt.Sprintf("window %d: printed %d words", resp.Seq, printed))
	}
	return nil
}

type terminalWordWriter struct {
	out        io.Writer
	columns    *terminalColumns
	col        int
	stopResize func()
}

type terminalColumns struct {
	fd       uintptr
	width    atomic.Int32
	enabled  bool
	provider func(uintptr) (int, bool)
}

func newTerminalWordWriter(file *os.File) *terminalWordWriter {
	columns := newTerminalColumns(file)
	stopResize := startTerminalResizeListener(context.Background(), columns, nil)
	return &terminalWordWriter{
		out:        file,
		columns:    columns,
		stopResize: stopResize,
	}
}

func newTerminalColumns(file *os.File) *terminalColumns {
	columns := &terminalColumns{
		fd:       file.Fd(),
		provider: terminalWidth,
	}
	if info, err := file.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		columns.enabled = true
		columns.refresh()
	}
	return columns
}

func newTestTerminalWordWriter(out io.Writer, width int) *terminalWordWriter {
	columns := &terminalColumns{enabled: width > 0}
	columns.width.Store(int32(width))
	return &terminalWordWriter{out: out, columns: columns}
}

func (w *terminalWordWriter) close() {
	if w.stopResize != nil {
		w.stopResize()
	}
}

func (w *terminalWordWriter) writeWord(text string, trailingSpace bool) error {
	width := w.columns.current()
	textWidth := displayWidth(text)
	spaceWidth := 0
	if trailingSpace {
		spaceWidth = 1
	}
	if width > 0 && w.col > 0 && w.col+textWidth+spaceWidth > width {
		if _, err := fmt.Fprint(w.out, "\n"); err != nil {
			return err
		}
		w.col = 0
	}
	if _, err := fmt.Fprint(w.out, text); err != nil {
		return err
	}
	w.col += textWidth
	if trailingSpace {
		if _, err := fmt.Fprint(w.out, " "); err != nil {
			return err
		}
		w.col += spaceWidth
	}
	return nil
}

func (c *terminalColumns) current() int {
	if c == nil || !c.enabled {
		return 0
	}
	return int(c.width.Load())
}

func (c *terminalColumns) refresh() {
	if c == nil || !c.enabled || c.provider == nil {
		return
	}
	if width, ok := c.provider(c.fd); ok && width > 0 {
		c.width.Store(int32(width))
	}
}

func startTerminalResizeListener(ctx context.Context, columns *terminalColumns, signals chan os.Signal) func() {
	if columns == nil || !columns.enabled {
		return nil
	}
	if signals == nil {
		signals = make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGWINCH)
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-signals:
				columns.refresh()
			}
		}
	}()
	return func() {
		cancel()
		signal.Stop(signals)
	}
}

type winsize struct {
	row    uint16
	col    uint16
	xpixel uint16
	ypixel uint16
}

func terminalWidth(fd uintptr) (int, bool) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.col == 0 {
		return 0, false
	}
	return int(ws.col), true
}

func displayWidth(text string) int {
	width := 0
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r':
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		case isWideRune(r):
			width += 2
		default:
			width++
		}
	}
	return width
}

func isWideRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2329 && r <= 0x232A) ||
		(r >= 0x2E80 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE19) ||
		(r >= 0xFE30 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6)
}

func appendStableWords(resp whisperResponse, stableUntil float64, printedUntil *float64, out *[]string) {
	for _, word := range resp.Words {
		start := resp.Offset + word.Start
		end := resp.Offset + word.End
		if end <= *printedUntil+0.05 || start < *printedUntil-0.25 || end > stableUntil {
			continue
		}
		text := strings.TrimSpace(word.Word)
		if text == "" {
			continue
		}
		*out = append(*out, text)
		*printedUntil = end
	}
}

func liveWhisperWorkerScript() string {
	return `
import base64
import json
import sys
import traceback

import numpy as np
import whisper

model_name = sys.argv[1]
model_dir = sys.argv[2]
language = sys.argv[3]
model = whisper.load_model(model_name, download_root=model_dir)

for line in sys.stdin:
    try:
        req = json.loads(line)
        raw = base64.b64decode(req["pcm16_b64"])
        audio = np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0
        result = model.transcribe(
            audio,
            language=language,
            task="transcribe",
            word_timestamps=True,
            fp16=False,
            verbose=None,
            condition_on_previous_text=False,
            initial_prompt=req.get("initial_prompt") or None,
        )
        words = []
        for segment in result.get("segments", []):
            for word in segment.get("words", []):
                words.append({
                    "word": word.get("word", ""),
                    "start": float(word.get("start", 0.0)),
                    "end": float(word.get("end", 0.0)),
                    "probability": float(word.get("probability", 0.0)),
                })
        print(json.dumps({
            "seq": req["seq"],
            "offset": req["offset"],
            "duration": req["duration"],
            "text": result.get("text", ""),
            "words": words,
        }, ensure_ascii=False), flush=True)
    except Exception as exc:
        print(json.dumps({
            "seq": req.get("seq", 0) if "req" in locals() else 0,
            "offset": req.get("offset", 0.0) if "req" in locals() else 0.0,
            "duration": req.get("duration", 0.0) if "req" in locals() else 0.0,
            "error": str(exc),
            "text": "",
            "words": [],
        }), flush=True)
        traceback.print_exc(file=sys.stderr)
`
}

func liveMLXWorkerScript() string {
	return `
import base64
import json
import sys
import traceback

import numpy as np
import mlx_whisper

model_name = sys.argv[1]
language = sys.argv[3]

for line in sys.stdin:
    try:
        req = json.loads(line)
        raw = base64.b64decode(req["pcm16_b64"])
        audio = np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0
        result = mlx_whisper.transcribe(
            audio,
            path_or_hf_repo=model_name,
            language=language,
            task="transcribe",
            word_timestamps=True,
            verbose=False,
            condition_on_previous_text=False,
            hallucination_silence_threshold=1.0,
            initial_prompt=req.get("initial_prompt") or None,
        )
        words = []
        for segment in result.get("segments", []):
            for word in segment.get("words") or []:
                words.append({
                    "word": word.get("word", ""),
                    "start": float(word.get("start", 0.0)),
                    "end": float(word.get("end", 0.0)),
                    "probability": float(word.get("probability", 0.0)),
                })
        print(json.dumps({
            "seq": req["seq"],
            "offset": req["offset"],
            "duration": req["duration"],
            "text": result.get("text", ""),
            "words": words,
        }, ensure_ascii=False), flush=True)
    except Exception as exc:
        print(json.dumps({
            "seq": req.get("seq", 0) if "req" in locals() else 0,
            "offset": req.get("offset", 0.0) if "req" in locals() else 0.0,
            "duration": req.get("duration", 0.0) if "req" in locals() else 0.0,
            "error": str(exc),
            "text": "",
            "words": [],
        }), flush=True)
        traceback.print_exc(file=sys.stderr)
`
}

func transcribe(ctx context.Context, cfg config, model, audioPath string) (whisperOutput, error) {
	if cfg.backend == backendMLX {
		return transcribeMLX(ctx, cfg, model, audioPath)
	}
	outDir := filepath.Join(cfg.workDir, "transcripts", model)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return whisperOutput{}, err
	}
	args := []string{
		audioPath,
		"--model", model,
		"--model_dir", filepath.Join(cfg.workDir, "models"),
		"--language", cfg.language,
		"--task", "transcribe",
		"--output_format", "json",
		"--output_dir", outDir,
		"--verbose", "False",
		"--word_timestamps", "True",
		"--fp16", "False",
		"--condition_on_previous_text", "False",
	}
	if cfg.initialPrompt != "" {
		args = append(args, "--initial_prompt", cfg.initialPrompt)
	}
	cmd := exec.CommandContext(ctx, cfg.whisperBin, args...)
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

func transcribeMLX(ctx context.Context, cfg config, model, audioPath string) (whisperOutput, error) {
	outDir := filepath.Join(cfg.workDir, "transcripts", "mlx-"+model)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return whisperOutput{}, err
	}
	args := []string{
		"-u", "-c", mlxTranscribeScript(),
		audioPath,
		mlxModelRef(model),
		cfg.language,
		cfg.initialPrompt,
	}
	cmd := exec.CommandContext(ctx, cfg.pythonBin, args...)
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

func mlxTranscribeScript() string {
	return `
import json
import sys

import mlx_whisper

audio_path = sys.argv[1]
model_name = sys.argv[2]
language = sys.argv[3]
initial_prompt = sys.argv[4] or None

result = mlx_whisper.transcribe(
    audio_path,
    path_or_hf_repo=model_name,
    language=language,
    task="transcribe",
    word_timestamps=True,
    verbose=False,
    condition_on_previous_text=False,
    hallucination_silence_threshold=1.0,
    initial_prompt=initial_prompt,
)
print("TR1_JSON:" + json.dumps(result, ensure_ascii=False), flush=True)
`
}

func runPreview(ctx context.Context, cfg config) error {
	fixture, transcript, err := loadFixture(cfg)
	if err != nil {
		return err
	}
	if cfg.previewOut == "" {
		cfg.previewOut = fixture.AudioPath
	}
	if err := requireBinaries(cfg.sagBin); err != nil {
		return err
	}
	if os.Getenv("ELEVENLABS_API_KEY") == "" && os.Getenv("ELEVENLABS_API_KEY_FILE") == "" {
		return fmt.Errorf("set ELEVENLABS_API_KEY or ELEVENLABS_API_KEY_FILE before running preview")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.previewOut), 0o755); err != nil {
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
		"--output", cfg.previewOut,
		transcript,
	}
	if cfg.sagVoice != "" {
		args = append(args[:1], append([]string{"--voice", cfg.sagVoice}, args[1:]...)...)
	}
	cmd := exec.CommandContext(ctx, cfg.sagBin, args...)
	cmd.Stdout = io.Discard
	if cfg.verbose {
		cmd.Stdout = os.Stderr
	}
	cmd.Stderr = prefixedStderr(cfg, "sag")
	status(cfg, "preview", "generating "+cfg.previewOut)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	status(cfg, "preview", "wrote "+cfg.previewOut)
	return nil
}

func runLocalPreview(ctx context.Context, cfg config) error {
	fixture, transcript, err := loadFixture(cfg)
	if err != nil {
		return err
	}
	if cfg.previewOut == "" {
		cfg.previewOut = fixture.AudioPath
	}
	if err := requireBinaries("say", cfg.ffmpegBin); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.previewOut), 0o755); err != nil {
		return err
	}

	aiffPath := strings.TrimSuffix(cfg.previewOut, filepath.Ext(cfg.previewOut)) + ".aiff"
	say := exec.CommandContext(ctx, "say", "-v", "Zosia", "-r", "178", "-o", aiffPath, transcript)
	say.Stderr = prefixedStderr(cfg, "say")
	status(cfg, "preview-local", "generating "+aiffPath)
	if err := say.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	ffmpeg := exec.CommandContext(ctx, cfg.ffmpegBin,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", aiffPath,
		"-ac", "1", "-ar", "16000",
		cfg.previewOut,
	)
	ffmpeg.Stderr = prefixedStderr(cfg, "ffmpeg")
	status(cfg, "preview-local", "encoding "+cfg.previewOut)
	if err := ffmpeg.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	status(cfg, "preview-local", "wrote "+cfg.previewOut)
	return nil
}

func runBenchmark(ctx context.Context, cfg config) error {
	if err := validateBackend(cfg.backend); err != nil {
		return err
	}
	if err := validateBenchmarkConfig(cfg); err != nil {
		return err
	}
	fixture, transcript, err := loadFixture(cfg)
	if err != nil {
		return err
	}
	if cfg.previewOut == "" {
		cfg.previewOut = fixture.AudioPath
	}
	backend, err := prepareBackend(ctx, &cfg)
	if err != nil {
		return err
	}
	audio := cfg.previewOut
	if _, err := os.Stat(audio); err != nil {
		return fmt.Errorf("%s not found; run `go run ./cmd/tr1 preview --fixture %s` first", audio, fixture.Name)
	}
	if err := ensureDirs(cfg); err != nil {
		return err
	}

	models := splitCSV(cfg.models)
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

	fmt.Println("BACKEND\tMODEL\tWER\tWORDS\tTIME\tRTF\tTEXT")
	for _, r := range results {
		fmt.Printf("%s\t%s\t%.3f\t%d\t%s\t%.3f\t%s\n", r.Backend, r.Model, r.WER, r.Words, r.Duration.Round(time.Millisecond), r.RTF, oneLine(r.Text))
	}
	return nil
}

func validateBenchmarkConfig(cfg config) error {
	switch strings.ToLower(strings.TrimSpace(cfg.benchmarkMode)) {
	case benchmarkModeWhole, benchmarkModeChunked:
	default:
		return fmt.Errorf("--mode must be one of: whole, chunked")
	}
	if strings.ToLower(strings.TrimSpace(cfg.benchmarkMode)) == benchmarkModeChunked {
		return validateStreamConfig(cfg)
	}
	return nil
}

func transcribeForBenchmark(ctx context.Context, cfg config, model, audioPath string) (whisperOutput, error) {
	if strings.ToLower(strings.TrimSpace(cfg.benchmarkMode)) == benchmarkModeChunked {
		chunkedCfg := cfg
		chunkedCfg.model = model
		chunkedCfg.wordDelay = 0
		text, err := transcribeChunkedFile(ctx, chunkedCfg, audioPath)
		if err != nil {
			return whisperOutput{}, err
		}
		return whisperOutput{Text: text}, nil
	}
	return transcribe(ctx, cfg, model, audioPath)
}

func transcribeChunkedFile(ctx context.Context, cfg config, audioPath string) (string, error) {
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
	windowBytes := cfg.chunkSeconds * sampleRate * bytesPerSample
	stepBytes := cfg.stepSeconds * sampleRate * bytesPerSample
	holdbackSeconds := cfg.holdback.Seconds()

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
			InitialPrompt: cfg.initialPrompt,
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

func loadFixture(cfg config) (benchmarkFixture, string, error) {
	fixture, err := lookupFixture(cfg.fixture)
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

func applyStationArg(cfg *config, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("expected at most one station, got %d", len(args))
	}
	cfg.station = args[0]
	return nil
}

func streamSelection(cfg config) (string, string, error) {
	if cfg.streamURL != "" {
		return "custom stream", cfg.streamURL, nil
	}
	selected, err := lookupStation(cfg.station)
	if err != nil {
		return "", "", err
	}
	return selected.Name, selected.URL, nil
}

func lookupStation(query string) (station, error) {
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
	return station{}, fmt.Errorf("unknown station %q (try one of: %s)", query, stationHelp())
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

func ensureDirs(cfg config) error {
	for _, dir := range []string{
		cfg.workDir,
		filepath.Join(cfg.workDir, "models"),
		filepath.Join(cfg.workDir, "transcripts"),
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

func prepareBackend(ctx context.Context, cfg *config) (string, error) {
	backend := strings.ToLower(strings.TrimSpace(cfg.backend))
	if backend == backendAuto {
		if mlxSupportedHardware() {
			if _, err := exec.LookPath(cfg.uvBin); err == nil {
				backend = backendMLX
			} else {
				backend = backendCPU
			}
		} else {
			backend = backendCPU
		}
	}
	cfg.backend = backend
	switch backend {
	case backendCPU:
		if err := requireBinaries(cfg.ffmpegBin, cfg.whisperBin); err != nil {
			return "", err
		}
		pythonBin, err := whisperPythonBin(*cfg)
		if err != nil {
			return "", err
		}
		cfg.pythonBin = pythonBin
		return backendCPU, nil
	case backendMLX:
		if err := requireBinaries(cfg.ffmpegBin, cfg.uvBin); err != nil {
			return "", err
		}
		pythonBin, err := ensureMLXEnv(ctx, *cfg)
		if err != nil {
			return "", err
		}
		cfg.pythonBin = pythonBin
		return backendMLX, nil
	default:
		return "", fmt.Errorf("--backend must be one of: auto, cpu, mlx")
	}
}

func mlxSupportedHardware() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

func ensureMLXEnv(ctx context.Context, cfg config) (string, error) {
	projectDir := filepath.Join(cfg.workDir, "mlx")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return "", err
	}
	pyprojectPath := filepath.Join(projectDir, "pyproject.toml")
	if err := os.WriteFile(pyprojectPath, []byte(embeddedPyproject), 0o644); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, cfg.uvBin, "sync", "--project", projectDir, "--quiet")
	cmd.Stderr = prefixedStderr(cfg, "uv")
	if cfg.verbose {
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

func whisperPythonBin(cfg config) (string, error) {
	if cfg.pythonBin != "" {
		if _, err := exec.LookPath(cfg.pythonBin); err != nil {
			return "", fmt.Errorf("%s not found in PATH", cfg.pythonBin)
		}
		return cfg.pythonBin, nil
	}
	whisperPath, err := exec.LookPath(cfg.whisperBin)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH", cfg.whisperBin)
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

func needsSpace(s string) bool {
	if s == "" {
		return false
	}
	last := []rune(s)[len([]rune(s))-1]
	return !strings.ContainsRune("([{/", last)
}

func oneLine(s string) string {
	re := regexp.MustCompile(`\s+`)
	s = re.ReplaceAllString(strings.TrimSpace(s), " ")
	if len([]rune(s)) > 180 {
		return string([]rune(s)[:180]) + "..."
	}
	return s
}

type prefixWriter struct {
	prefix string
	buf    []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		if line != "" {
			fmt.Fprintf(os.Stderr, "\n[%s] %s\n", w.prefix, line)
		}
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
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

func status(cfg config, scope, msg string) {
	if !cfg.verbose {
		return
	}
	fmt.Fprintf(os.Stderr, "\n[%s] %s\n", scope, msg)
}

func prefixedStderr(cfg config, prefix string) io.Writer {
	if !cfg.verbose {
		return io.Discard
	}
	return &prefixWriter{prefix: prefix}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "tr1:", err)
	os.Exit(1)
}

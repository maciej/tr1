package main

import (
	"bufio"
	"bytes"
	"context"
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
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/spf13/cobra"
)

const (
	defaultPlaylistURL  = "http://www.tuba.fm/stream.pls?radio=10&mp3=1"
	defaultWorkDir      = ".tr1"
	referenceTranscript = "Dzień dobry, to jest specjalny serwis informacyjny. " +
		"W Warszawie rozpoczęły się rozmowy o nowych inwestycjach w energetykę i transport publiczny. " +
		"Rząd zapowiada dodatkowe środki dla samorządów, a ekonomiści podkreślają znaczenie stabilnych cen. " +
		"Po południu spodziewane są konferencje prasowe oraz najnowsze dane z rynku pracy."
)

type config struct {
	streamURL     string
	model         string
	models        string
	language      string
	chunkSeconds  int
	stepSeconds   int
	holdback      time.Duration
	workDir       string
	whisperBin    string
	pythonBin     string
	ffmpegBin     string
	sagBin        string
	sagVoice      string
	wordDelay     time.Duration
	previewOut    string
	initialPrompt string
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
	Model    string
	WER      float64
	Words    int
	Duration time.Duration
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
		streamURL:     getenv("TR1_STREAM_URL", defaultPlaylistURL),
		model:         getenv("TR1_MODEL", "base"),
		models:        getenv("TR1_MODELS", "tiny,base"),
		language:      getenv("TR1_LANGUAGE", "Polish"),
		chunkSeconds:  intFromEnv("TR1_CHUNK_SECONDS", 12),
		stepSeconds:   intFromEnv("TR1_STEP_SECONDS", 3),
		holdback:      durationFromEnv("TR1_HOLDBACK", 1500*time.Millisecond),
		workDir:       getenv("TR1_WORKDIR", defaultWorkDir),
		whisperBin:    getenv("TR1_WHISPER_BIN", "whisper"),
		pythonBin:     getenv("TR1_PYTHON_BIN", ""),
		ffmpegBin:     getenv("TR1_FFMPEG_BIN", "ffmpeg"),
		sagBin:        getenv("TR1_SAG_BIN", "sag"),
		sagVoice:      getenv("TR1_SAG_VOICE", ""),
		wordDelay:     durationFromEnv("TR1_WORD_DELAY", 35*time.Millisecond),
		previewOut:    filepath.Join("assets", "news-preview.mp3"),
		initialPrompt: "Polski serwis informacyjny Radia TOK FM. Poprawna polska interpunkcja i nazwy własne.",
		verbose:       boolFromEnv("TR1_VERBOSE", false),
	}
}

func newRootCommand(ctx context.Context) *cobra.Command {
	cfg := defaultConfig()

	rootCmd := &cobra.Command{
		Use:           "tr1",
		Short:         "Terminal TOK FM receiver and Whisper transcription loop",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q", args[0])
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
		Use:   "stream",
		Short: "Stream TOK FM audio and print live Whisper transcription",
		Args:  cobra.NoArgs,
		RunE:  command(validateStreamConfig, runStream),
	}
	addStreamFlags(streamCmd, &cfg)

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

	rootCmd.AddCommand(
		streamCmd,
		previewCmd,
		localPreviewCmd,
		benchmarkCmd,
	)

	return rootCmd
}

func addStreamFlags(cmd *cobra.Command, cfg *config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.streamURL, "stream-url", cfg.streamURL, "radio stream or playlist URL")
	flags.StringVar(&cfg.model, "model", cfg.model, "Whisper model")
	flags.StringVar(&cfg.language, "language", cfg.language, "Whisper language")
	flags.IntVar(&cfg.chunkSeconds, "window", cfg.chunkSeconds, "rolling transcription window in seconds")
	flags.IntVar(&cfg.stepSeconds, "step", cfg.stepSeconds, "seconds of new audio between Whisper requests")
	flags.StringVar(&cfg.workDir, "workdir", cfg.workDir, "runtime working directory")
	flags.StringVar(&cfg.whisperBin, "whisper-bin", cfg.whisperBin, "whisper executable")
	flags.StringVar(&cfg.pythonBin, "python-bin", cfg.pythonBin, "python executable for live Whisper worker; defaults to the whisper CLI interpreter")
	flags.StringVar(&cfg.ffmpegBin, "ffmpeg-bin", cfg.ffmpegBin, "ffmpeg executable")
	flags.DurationVar(&cfg.wordDelay, "word-delay", cfg.wordDelay, "delay between printed words")
	flags.DurationVar(&cfg.holdback, "holdback", cfg.holdback, "hold back live words near the unstable end of each window")
	flags.StringVar(&cfg.initialPrompt, "initial-prompt", cfg.initialPrompt, "Whisper initial prompt")
}

func addPreviewFlags(cmd *cobra.Command, cfg *config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.previewOut, "preview-out", cfg.previewOut, "path for ElevenLabs preview audio")
	flags.StringVar(&cfg.sagBin, "sag-bin", cfg.sagBin, "sag executable")
	flags.StringVar(&cfg.sagVoice, "voice", cfg.sagVoice, "sag/ElevenLabs voice name or ID")
}

func addLocalPreviewFlags(cmd *cobra.Command, cfg *config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.previewOut, "preview-out", cfg.previewOut, "path for local preview audio")
	flags.StringVar(&cfg.ffmpegBin, "ffmpeg-bin", cfg.ffmpegBin, "ffmpeg executable")
}

func addBenchmarkFlags(cmd *cobra.Command, cfg *config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.models, "models", cfg.models, "comma-separated Whisper models")
	flags.StringVar(&cfg.previewOut, "preview-out", cfg.previewOut, "path for benchmark preview audio")
	flags.StringVar(&cfg.workDir, "workdir", cfg.workDir, "runtime working directory")
	flags.StringVar(&cfg.whisperBin, "whisper-bin", cfg.whisperBin, "whisper executable")
	flags.StringVar(&cfg.language, "language", cfg.language, "Whisper language")
	flags.StringVar(&cfg.initialPrompt, "initial-prompt", cfg.initialPrompt, "Whisper initial prompt")
}

func validateStreamConfig(cfg config) error {
	if cfg.chunkSeconds < 3 {
		return fmt.Errorf("--window must be at least 3 seconds")
	}
	if cfg.stepSeconds < 1 {
		return fmt.Errorf("--step must be at least 1 second")
	}
	if cfg.stepSeconds > cfg.chunkSeconds {
		return fmt.Errorf("--step must be less than or equal to --window")
	}
	return nil
}

func runStream(ctx context.Context, cfg config) error {
	if err := requireBinaries(cfg.ffmpegBin, cfg.whisperBin); err != nil {
		return err
	}
	pythonBin, err := whisperPythonBin(cfg)
	if err != nil {
		return err
	}
	cfg.pythonBin = pythonBin
	if err := ensureDirs(cfg); err != nil {
		return err
	}

	streamURL, err := resolveStreamURL(ctx, cfg.streamURL)
	if err != nil {
		return err
	}
	status(cfg, "stream", "using "+streamURL)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return streamToWhisper(ctx, cfg, streamURL)
}

func streamToWhisper(ctx context.Context, cfg config, streamURL string) error {
	ffmpegStdout, ffmpegDone, err := startPCMStream(ctx, cfg, streamURL)
	if err != nil {
		return err
	}
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
			} else if err := printStableWords(ctx, cfg, resp, windowEnd-holdbackSeconds, &printedUntil); err != nil {
				return err
			}
			nextPrintSeq++
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
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

func startWhisperWorker(cfg config) (*liveWhisperWorker, error) {
	args := []string{"-u", "-c", liveWhisperWorkerScript(), cfg.model, filepath.Join(cfg.workDir, "models"), cfg.language}
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
		w.errors <- err
	}
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

func printStableWords(ctx context.Context, cfg config, resp whisperResponse, stableUntil float64, printedUntil *float64) error {
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
		fmt.Print(text)
		if needsSpace(text) {
			fmt.Print(" ")
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

func transcribe(ctx context.Context, cfg config, model, audioPath string) (whisperOutput, error) {
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

func runPreview(ctx context.Context, cfg config) error {
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
		referenceTranscript,
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
	if err := requireBinaries("say", cfg.ffmpegBin); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.previewOut), 0o755); err != nil {
		return err
	}

	aiffPath := strings.TrimSuffix(cfg.previewOut, filepath.Ext(cfg.previewOut)) + ".aiff"
	say := exec.CommandContext(ctx, "say", "-v", "Zosia", "-r", "178", "-o", aiffPath, referenceTranscript)
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
	if err := requireBinaries(cfg.whisperBin); err != nil {
		return err
	}
	audio := cfg.previewOut
	if _, err := os.Stat(audio); err != nil {
		return fmt.Errorf("%s not found; run `go run ./cmd/tr1 preview` first", audio)
	}
	if err := ensureDirs(cfg); err != nil {
		return err
	}

	models := splitCSV(cfg.models)
	if len(models) == 0 {
		return fmt.Errorf("no models supplied")
	}

	results := make([]benchResult, 0, len(models))
	for _, model := range models {
		status(cfg, "benchmark", "running model "+model)
		start := time.Now()
		out, err := transcribe(ctx, cfg, model, audio)
		if err != nil {
			return err
		}
		text := normalizeText(out.Text)
		ref := normalizeText(referenceTranscript)
		result := benchResult{
			Model:    model,
			WER:      wordErrorRate(strings.Fields(ref), strings.Fields(text)),
			Words:    len(strings.Fields(text)),
			Duration: time.Since(start),
			Text:     strings.TrimSpace(out.Text),
		}
		results = append(results, result)
	}

	fmt.Println("MODEL\tWER\tWORDS\tTIME\tTEXT")
	for _, r := range results {
		fmt.Printf("%s\t%.3f\t%d\t%s\t%s\n", r.Model, r.WER, r.Words, r.Duration.Round(time.Millisecond), oneLine(r.Text))
	}
	return nil
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

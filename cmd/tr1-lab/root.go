package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"tr1/internal/programmes"
	"tr1/internal/programmes/tokfm"
	"tr1/internal/tr1"
)

const defaultProgrammeTimeout = 15 * time.Second

func newLabCommand(ctx context.Context) *cobra.Command {
	cfg := tr1.DefaultConfig()

	rootCmd := &cobra.Command{
		Use:           "tr1-lab",
		Short:         "Developer tools for tr1 fixtures and benchmarks",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.PersistentFlags().BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "print diagnostic status messages to stderr")

	command := func(validate func(tr1.Config) error, run func(context.Context, tr1.Config) error) func(*cobra.Command, []string) error {
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

	previewCmd := &cobra.Command{
		Use:   "preview",
		Short: "Generate synthetic news-broadcast preview audio with sag",
		Args:  cobra.NoArgs,
		RunE:  command(nil, tr1.RunPreview),
	}
	addPreviewFlags(previewCmd, &cfg)

	benchmarkCmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Benchmark Whisper models against the generated preview audio",
		Args:  cobra.NoArgs,
		RunE:  command(nil, tr1.RunBenchmark),
	}
	addBenchmarkFlags(benchmarkCmd, &cfg)

	recordCmd := &cobra.Command{
		Use:   "record [station]",
		Short: "Record internet radio broadcasts into the tr1 cache",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyStationArg(&cfg, args); err != nil {
				return err
			}
			if err := tr1.ValidateRecordConfig(cfg); err != nil {
				return err
			}
			return tr1.RunRecord(ctx, cfg)
		},
	}
	addRecordFlags(recordCmd, &cfg)

	recordListStation := ""
	recordListCmd := &cobra.Command{
		Use:   "list [station]",
		Short: "List cached broadcast recordings",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				cfg.Station = args[0]
			} else if !cmd.Flags().Changed("station") {
				cfg.Station = ""
			} else {
				cfg.Station = recordListStation
			}
			return tr1.RunRecordList(cmd.OutOrStdout(), cfg)
		},
	}
	addRecordListFlags(recordListCmd, &cfg, &recordListStation)
	recordCmd.AddCommand(recordListCmd)

	recordTranscribeCmd := &cobra.Command{
		Use:   "transcribe [recording-path-or-station]",
		Short: "Transcribe a cached recording and keep versioned transcript output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Station = args[0]
			markLanguageOverride(&cfg, cmd)
			if err := tr1.ValidateRecordTranscribeConfig(cfg); err != nil {
				return err
			}
			return tr1.RunRecordTranscribe(cmd.OutOrStdout(), ctx, cfg, args[0])
		},
	}
	addRecordTranscribeFlags(recordTranscribeCmd, &cfg)
	recordCmd.AddCommand(recordTranscribeCmd)

	programmesCmd := newProgrammesCommand(ctx, &cfg)

	rootCmd.AddCommand(previewCmd, benchmarkCmd, recordCmd, programmesCmd)

	return rootCmd
}

func newProgrammesCommand(ctx context.Context, cfg *tr1.Config) *cobra.Command {
	sourceURL := getenv("TR1_PROGRAMME_SOURCE_URL", tokfm.DefaultScheduleURL)
	format := getenv("TR1_PROGRAMME_FORMAT", "table")
	timeout := durationFromEnv("TR1_PROGRAMME_TIMEOUT", defaultProgrammeTimeout)

	cmd := &cobra.Command{
		Use:     "programmes [station]",
		Aliases: []string{"programs", "schedule"},
		Short:   "Fetch a station programme schedule",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyStationArg(cfg, args); err != nil {
				return err
			}
			if err := validateProgrammesConfig(*cfg, sourceURL, format, timeout); err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			schedule, err := tokfm.Fetch(ctx, http.DefaultClient, sourceURL)
			if err != nil {
				return err
			}
			cacheRoot, err := tr1.CacheRoot(cfg.CacheDir)
			if err != nil {
				return err
			}
			cacheResult, err := programmes.NewSQLiteStore(programmes.CachePath(cacheRoot)).PutSchedule(ctx, schedule)
			if err != nil {
				return err
			}
			status(*cfg, "programme-cache", fmt.Sprintf("stored %d new entries in %s", cacheResult.NewEntries, cacheResult.Path))

			switch strings.ToLower(strings.TrimSpace(format)) {
			case "json":
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(schedule)
			default:
				return programmes.WriteTable(cmd.OutOrStdout(), schedule)
			}
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&cfg.Station, "station", "s", cfg.Station, "station alias or canonical name")
	flags.StringVar(&format, "format", format, "output format: table or json")
	flags.StringVar(&sourceURL, "source-url", sourceURL, "programme schedule page URL")
	flags.StringVar(&cfg.CacheDir, "cache-dir", cfg.CacheDir, "cache directory; defaults to $XDG_CACHE_HOME/tr1 or ~/.cache/tr1")
	flags.DurationVar(&timeout, "timeout", timeout, "HTTP timeout for programme schedule fetches")
	cmd.AddCommand(newProgrammesTranscribeCommand(ctx, cfg, &sourceURL, &timeout))
	return cmd
}

func newProgrammesTranscribeCommand(ctx context.Context, cfg *tr1.Config, sourceURL *string, timeout *time.Duration) *cobra.Command {
	opts := tr1.DefaultProgrammeTranscribeOptions()
	refreshSchedule := false
	cmd := &cobra.Command{
		Use:   "transcribe [station]",
		Short: "Cut cached TOK FM recordings into programme windows and transcribe them",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyStationArg(cfg, args); err != nil {
				return err
			}
			opts.Station = cfg.Station
			markLanguageOverride(cfg, cmd)
			if err := validateProgrammesConfig(*cfg, *sourceURL, "json", *timeout); err != nil {
				return err
			}
			schedule, err := loadTokFMSchedule(ctx, *cfg, *sourceURL, *timeout, refreshSchedule)
			if err != nil {
				return err
			}
			opts.Schedule = schedule
			opts.Force = cfg.TranscribeForce
			if err := tr1.ValidateProgrammeTranscribeConfig(*cfg, opts); err != nil {
				return err
			}
			return tr1.RunProgrammeTranscribe(cmd.OutOrStdout(), ctx, *cfg, opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&cfg.CacheDir, "cache-dir", cfg.CacheDir, "cache directory; defaults to $XDG_CACHE_HOME/tr1 or ~/.cache/tr1")
	flags.StringVar(&cfg.Model, "model", cfg.Model, "Whisper model")
	flags.StringVar(&cfg.Language, "language", cfg.Language, "Whisper language; defaults to the recording station language when known")
	flags.StringVar(&cfg.Backend, "backend", cfg.Backend, "transcription backend: auto, cpu, or mlx")
	flags.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "runtime working directory")
	flags.StringVar(&cfg.WhisperBin, "whisper-bin", cfg.WhisperBin, "whisper executable")
	flags.StringVar(&cfg.UVBin, "uv-bin", cfg.UVBin, "uv executable used to provision the MLX Python runtime")
	flags.StringVar(&cfg.PythonBin, "python-bin", cfg.PythonBin, "python executable for live Whisper worker; defaults to the whisper CLI interpreter")
	flags.StringVar(&cfg.FFmpegBin, "ffmpeg-bin", cfg.FFmpegBin, "ffmpeg executable")
	flags.StringVar(&cfg.InitialPrompt, "initial-prompt", cfg.InitialPrompt, "Whisper initial prompt")
	flags.BoolVar(&cfg.TranscribeForce, "force", cfg.TranscribeForce, "recompute programme audio and transcript even when cached")
	flags.StringVar(&opts.From, "from", opts.From, "only transcribe programmes ending after this local/RFC3339 time")
	flags.StringVar(&opts.To, "to", opts.To, "only transcribe programmes starting before this local/RFC3339 time")
	flags.IntVar(&opts.Limit, "limit", opts.Limit, "maximum number of programme windows to process; 0 means all")
	flags.DurationVar(&opts.StreamDelay, "stream-delay", opts.StreamDelay, "internet stream delay relative to schedule; positive means captured audio lags")
	flags.DurationVar(&opts.PreRoll, "pre-roll", opts.PreRoll, "extra audio before each scheduled programme boundary")
	flags.DurationVar(&opts.PostRoll, "post-roll", opts.PostRoll, "extra audio after each scheduled programme boundary")
	flags.DurationVar(&opts.MinDuration, "min-duration", opts.MinDuration, "skip programme windows with less cached audio than this")
	flags.DurationVar(&opts.TranscribeChunk, "transcribe-chunk-duration", opts.TranscribeChunk, "internal chunk size for long programme transcription; 0 disables chunking")
	flags.StringVar(&opts.WindowMode, "window-mode", opts.WindowMode, "schedule windowing mode: programme or segment")
	flags.StringVar(&opts.Timezone, "timezone", opts.Timezone, "programme schedule timezone")
	flags.BoolVar(&opts.PriorityOnly, "priority-only", opts.PriorityOnly, "only process talk/news/political programme windows")
	flags.BoolVar(&opts.PlanOnly, "plan-only", opts.PlanOnly, "show programme windows that would be processed without cutting audio or transcribing")
	flags.BoolVar(&opts.Diarize, "diarize", opts.Diarize, "run pyannote speaker diarization; use --diarize=false to skip it")
	flags.BoolVar(&opts.SpeakerMap, "speaker-map", opts.SpeakerMap, "ask codex mini to map speaker labels and clean ads/music; use --speaker-map=false to skip it")
	flags.StringVar(&opts.PyannotePythonBin, "pyannote-python-bin", opts.PyannotePythonBin, "python executable with pyannote.audio installed")
	flags.StringVar(&opts.PyannoteModel, "pyannote-model", opts.PyannoteModel, "pyannote diarization pipeline repo")
	flags.StringVar(&opts.PyannoteDevice, "pyannote-device", opts.PyannoteDevice, "pyannote torch device: auto, cpu, mps, or cuda")
	flags.StringVar(&opts.CodexBin, "codex-bin", opts.CodexBin, "codex CLI executable used for speaker-name mapping")
	flags.StringVar(&opts.CodexModel, "codex-model", opts.CodexModel, "codex model used for speaker-name mapping")
	flags.StringVar(sourceURL, "source-url", *sourceURL, "programme schedule page URL")
	flags.DurationVar(timeout, "timeout", *timeout, "HTTP timeout for programme schedule fetches")
	flags.BoolVar(&refreshSchedule, "refresh-schedule", refreshSchedule, "fetch the TOK FM schedule before transcribing")
	return cmd
}

func loadTokFMSchedule(ctx context.Context, cfg tr1.Config, sourceURL string, timeout time.Duration, refresh bool) (programmes.Schedule, error) {
	cacheRoot, err := tr1.CacheRoot(cfg.CacheDir)
	if err != nil {
		return programmes.Schedule{}, err
	}
	store := programmes.NewSQLiteStore(programmes.CachePath(cacheRoot))
	if !refresh {
		schedule, err := store.LatestSchedule(ctx, "TokFM")
		if err == nil {
			status(cfg, "programme-cache", "using cached schedule fetched at "+schedule.FetchedAt)
			return schedule, nil
		}
		status(cfg, "programme-cache", "no cached schedule available; fetching")
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	schedule, err := tokfm.Fetch(fetchCtx, http.DefaultClient, sourceURL)
	if err != nil {
		return programmes.Schedule{}, err
	}
	cacheResult, err := store.PutSchedule(ctx, schedule)
	if err != nil {
		return programmes.Schedule{}, err
	}
	status(cfg, "programme-cache", fmt.Sprintf("stored %d new entries in %s", cacheResult.NewEntries, cacheResult.Path))
	return schedule, nil
}

func validateProgrammesConfig(cfg tr1.Config, sourceURL, format string, timeout time.Duration) error {
	selected, err := tr1.LookupStation(cfg.Station)
	if err != nil {
		return err
	}
	if selected.Name != "TokFM" {
		return fmt.Errorf("programme schedules are currently available for tokfm only")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "table", "json":
	default:
		return fmt.Errorf("--format must be one of: table, json")
	}
	if strings.TrimSpace(sourceURL) == "" {
		return fmt.Errorf("--source-url must not be empty")
	}
	if timeout < time.Second {
		return fmt.Errorf("--timeout must be at least 1s")
	}
	return nil
}

func addPreviewFlags(cmd *cobra.Command, cfg *tr1.Config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.Fixture, "fixture", cfg.Fixture, "benchmark fixture name ("+tr1.FixtureHelp()+")")
	flags.StringVar(&cfg.PreviewOut, "preview-out", cfg.PreviewOut, "path for ElevenLabs preview audio")
	flags.StringVar(&cfg.SagBin, "sag-bin", cfg.SagBin, "sag executable")
	flags.StringVar(&cfg.SagVoice, "voice", cfg.SagVoice, "sag/ElevenLabs voice name or ID")
}

func addBenchmarkFlags(cmd *cobra.Command, cfg *tr1.Config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.Fixture, "fixture", cfg.Fixture, "benchmark fixture name ("+tr1.FixtureHelp()+")")
	flags.StringVar(&cfg.Models, "models", cfg.Models, "comma-separated Whisper models")
	flags.StringVar(&cfg.PreviewOut, "preview-out", cfg.PreviewOut, "path for benchmark preview audio")
	flags.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "runtime working directory")
	flags.StringVar(&cfg.Backend, "backend", cfg.Backend, "transcription backend: auto, cpu, or mlx")
	flags.StringVar(&cfg.WhisperBin, "whisper-bin", cfg.WhisperBin, "whisper executable")
	flags.StringVar(&cfg.UVBin, "uv-bin", cfg.UVBin, "uv executable used to provision the MLX Python runtime")
	flags.StringVar(&cfg.Language, "language", cfg.Language, "Whisper language")
	flags.StringVar(&cfg.InitialPrompt, "initial-prompt", cfg.InitialPrompt, "Whisper initial prompt")
	flags.StringVar(&cfg.BenchmarkMode, "mode", cfg.BenchmarkMode, "benchmark mode: whole or chunked")
	flags.IntVar(&cfg.ChunkSeconds, "window", cfg.ChunkSeconds, "rolling transcription window in seconds for chunked mode")
	flags.IntVar(&cfg.StepSeconds, "step", cfg.StepSeconds, "seconds of new audio between Whisper requests for chunked mode")
	flags.DurationVar(&cfg.Holdback, "holdback", cfg.Holdback, "hold back words near the unstable end of each window for chunked mode")
}

func addRecordFlags(cmd *cobra.Command, cfg *tr1.Config) {
	flags := cmd.Flags()
	flags.StringVarP(&cfg.Station, "station", "s", cfg.Station, "station alias or canonical name ("+tr1.StationHelp()+")")
	flags.StringVar(&cfg.StreamURL, "stream-url", cfg.StreamURL, "radio stream or playlist URL; overrides --station")
	flags.StringVar(&cfg.CacheDir, "cache-dir", cfg.CacheDir, "cache directory; defaults to $XDG_CACHE_HOME/tr1 or ~/.cache/tr1")
	flags.DurationVar(&cfg.RecordSegment, "segment-duration", cfg.RecordSegment, "recording chunk duration")
	flags.DurationVar(&cfg.RecordRestart, "restart-delay", cfg.RecordRestart, "delay before restarting ffmpeg after stream failure")
	flags.StringVar(&cfg.FFmpegBin, "ffmpeg-bin", cfg.FFmpegBin, "ffmpeg executable")
}

func addRecordListFlags(cmd *cobra.Command, cfg *tr1.Config, station *string) {
	flags := cmd.Flags()
	flags.StringVarP(station, "station", "s", "", "station alias or canonical name; omit to list all stations")
	flags.StringVar(&cfg.CacheDir, "cache-dir", cfg.CacheDir, "cache directory; defaults to $XDG_CACHE_HOME/tr1 or ~/.cache/tr1")
}

func addRecordTranscribeFlags(cmd *cobra.Command, cfg *tr1.Config) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.CacheDir, "cache-dir", cfg.CacheDir, "cache directory; defaults to $XDG_CACHE_HOME/tr1 or ~/.cache/tr1")
	flags.StringVar(&cfg.Model, "model", cfg.Model, "Whisper model")
	flags.StringVar(&cfg.Language, "language", cfg.Language, "Whisper language; defaults to the recording station language when known")
	flags.StringVar(&cfg.Backend, "backend", cfg.Backend, "transcription backend: auto, cpu, or mlx")
	flags.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "runtime working directory")
	flags.StringVar(&cfg.WhisperBin, "whisper-bin", cfg.WhisperBin, "whisper executable")
	flags.StringVar(&cfg.UVBin, "uv-bin", cfg.UVBin, "uv executable used to provision the MLX Python runtime")
	flags.StringVar(&cfg.PythonBin, "python-bin", cfg.PythonBin, "python executable for live Whisper worker; defaults to the whisper CLI interpreter")
	flags.StringVar(&cfg.FFmpegBin, "ffmpeg-bin", cfg.FFmpegBin, "ffmpeg executable")
	flags.StringVar(&cfg.InitialPrompt, "initial-prompt", cfg.InitialPrompt, "Whisper initial prompt")
	flags.BoolVar(&cfg.TranscribeForce, "force", cfg.TranscribeForce, "recompute transcript even when a cached version exists")
}

func applyStationArg(cfg *tr1.Config, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("expected at most one station, got %d", len(args))
	}
	cfg.Station = args[0]
	return nil
}

func markLanguageOverride(cfg *tr1.Config, cmd *cobra.Command) {
	if cmd != nil && cmd.Flags().Changed("language") {
		cfg.LanguageSet = true
	}
}

func status(cfg tr1.Config, scope, msg string) {
	if !cfg.Verbose {
		return
	}
	fmt.Fprintf(os.Stderr, "\n[%s] %s\n", scope, msg)
}

func getenv(name, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
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

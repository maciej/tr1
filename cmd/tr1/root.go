package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"tr1/internal/tr1"
)

func newRootCommand(ctx context.Context) *cobra.Command {
	cfg := tr1.DefaultConfig()

	rootCmd := &cobra.Command{
		Use:           "tr1 [station]",
		Short:         "Terminal radio receiver and Whisper transcription loop",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyStationArg(&cfg, args); err != nil {
				return err
			}
			markLanguageOverride(&cfg, cmd)
			if err := tr1.ValidateStreamConfig(cfg); err != nil {
				return err
			}
			return tr1.RunStream(ctx, cfg)
		},
	}

	rootCmd.PersistentFlags().BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "print diagnostic status messages to stderr")
	addStreamFlags(rootCmd, &cfg)

	command := func(validate func(tr1.Config) error, run func(context.Context, tr1.Config) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			markLanguageOverride(&cfg, cmd)
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
			markLanguageOverride(&cfg, cmd)
			return command(tr1.ValidateStreamConfig, tr1.RunStream)(cmd, nil)
		},
	}
	addStreamFlags(streamCmd, &cfg)

	stationsCmd := &cobra.Command{
		Use:   "stations",
		Short: "List available radio stations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tr1.WriteStations(cmd.OutOrStdout())
		},
	}

	rootCmd.AddCommand(streamCmd, stationsCmd)
	rootCmd.AddCommand(stationAliasCommands(&cfg, command(tr1.ValidateStreamConfig, tr1.RunStream))...)

	return rootCmd
}

func stationAliasCommands(cfg *tr1.Config, run func(*cobra.Command, []string) error) []*cobra.Command {
	seen := map[string]bool{}
	var out []*cobra.Command
	for _, s := range tr1.Stations() {
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
						cfg.Station = alias
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

func addStreamFlags(cmd *cobra.Command, cfg *tr1.Config) {
	flags := cmd.Flags()
	flags.StringVarP(&cfg.Station, "station", "s", cfg.Station, "station alias or canonical name ("+tr1.StationHelp()+")")
	flags.StringVar(&cfg.StreamURL, "stream-url", cfg.StreamURL, "radio stream or playlist URL; overrides --station")
	flags.StringVar(&cfg.Model, "model", cfg.Model, "Whisper model")
	flags.StringVar(&cfg.Language, "language", cfg.Language, "Whisper language")
	flags.StringVar(&cfg.Backend, "backend", cfg.Backend, "transcription backend: auto, cpu, or mlx")
	flags.IntVar(&cfg.ChunkSeconds, "window", cfg.ChunkSeconds, "rolling transcription window in seconds")
	flags.IntVar(&cfg.StepSeconds, "step", cfg.StepSeconds, "seconds of new audio between Whisper requests")
	flags.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "runtime working directory")
	flags.StringVar(&cfg.WhisperBin, "whisper-bin", cfg.WhisperBin, "whisper executable")
	flags.StringVar(&cfg.UVBin, "uv-bin", cfg.UVBin, "uv executable used to provision the MLX Python runtime")
	flags.StringVar(&cfg.PythonBin, "python-bin", cfg.PythonBin, "python executable for live Whisper worker; defaults to the whisper CLI interpreter")
	flags.StringVar(&cfg.FFmpegBin, "ffmpeg-bin", cfg.FFmpegBin, "ffmpeg executable")
	flags.StringVar(&cfg.FFplayBin, "ffplay-bin", cfg.FFplayBin, "ffplay executable used by --play")
	flags.BoolVar(&cfg.MonitorAudio, "play", cfg.MonitorAudio, "play live stream audio through the system audio output while transcribing")
	flags.DurationVar(&cfg.WordDelay, "word-delay", cfg.WordDelay, "delay between printed words")
	flags.BoolVar(&cfg.Spinner, "spinner", cfg.Spinner, "show an interactive waiting spinner between transcription updates")
	flags.DurationVar(&cfg.Holdback, "holdback", cfg.Holdback, "hold back live words near the unstable end of each window")
	flags.StringVar(&cfg.InitialPrompt, "initial-prompt", cfg.InitialPrompt, "Whisper initial prompt")
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

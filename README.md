# tr1

Terminal radio receiver and Whisper transcription loop.

## Run

```sh
go run ./cmd/tr1
```

For local environment variables, copy `.env.example` to `.env` and fill in local values. `.env` is ignored by git. The Make targets load it automatically:

```sh
make run
make preview
make benchmark
```

Stop with `Ctrl+C`.

The command resolves the TOK FM playlist at `http://www.tuba.fm/stream.pls?radio=10&mp3=1`, pipes raw 16 kHz PCM from `ffmpeg`, and feeds rolling audio windows into a persistent local Whisper worker. By default only transcript words are printed, streaming to stdout as stable word timestamps come back from Whisper.

TokFM is the default station. Pick another station with a short alias:

```sh
go run ./cmd/tr1 rmf
go run ./cmd/tr1 stream zet
go run ./cmd/tr1 --station trojka
go run ./cmd/tr1 bbc
```

Supported stations:

| Canonical name | Default language | Convenient aliases |
| --- | --- | --- |
| TokFM | Polish | `tokfm`, `tok`, `tok-fm` |
| Polskie Radio Jedynka | Polish | `jedynka`, `pr1`, `1` |
| Program Drugi Polskiego Radia | Polish | `dwojka`, `dwójka`, `pr2`, `2` |
| Trójka | Polish | `trojka`, `trójka`, `pr3`, `3` |
| RMF FM | Polish | `rmf`, `rmffm`, `rmf-fm` |
| Radio ZET | Polish | `zet`, `radiozet`, `radio-zet` |
| BBC World Service | English | `bbc`, `bbcws`, `english` |

Each station sets Whisper's default language. You can override it with `--language` / `TR1_LANGUAGE`. You can also set `TR1_STATION`, or pass `--stream-url` / `TR1_STREAM_URL` to use a custom stream URL directly.

## Requirements

- Go 1.26+
- `ffmpeg`
- `openai-whisper` CLI available as `whisper` for the CPU backend
- `uv` for the MLX backend on Apple Silicon
- `sag` only for generating the benchmark preview audio

On this machine, `ffmpeg`, `whisper`, `uv`, and `sag` are installed through Homebrew.

## Useful commands

Generate the synthetic news-broadcast preview audio with ElevenLabs through `sag`:

```sh
ELEVENLABS_API_KEY=... go run ./cmd/tr1-lab preview
```

Benchmark fixtures are selected by name. The default fixture is `biebrza-broadcast`; the shorter smoke-test fixture is `news-preview`:

```sh
ELEVENLABS_API_KEY=... go run ./cmd/tr1-lab preview --fixture biebrza-broadcast --voice <voice-name-or-id>
go run ./cmd/tr1-lab benchmark --fixture biebrza-broadcast --models tiny,base,small,medium
```

Record raw broadcast chunks for later lab work:

```sh
go run ./cmd/tr1-lab record tokfm
go run ./cmd/tr1-lab record --segment-duration 10m bbc
go run ./cmd/tr1-lab record list
go run ./cmd/tr1-lab record list tokfm
go run ./cmd/tr1-lab record transcribe bbc --model tiny
go run ./cmd/tr1-lab record transcribe ~/.cache/tr1/recordings/tokfm/2026/05/18/tokfm_20260518T120000Z.mka
```

Recordings are written under the tr1 cache root: `$XDG_CACHE_HOME/tr1` when `XDG_CACHE_HOME` is set, otherwise `~/.cache/tr1` including on macOS. Files are grouped by station and UTC date:

```text
~/.cache/tr1/recordings/tokfm/2026/05/18/tokfm_20260518T120000Z.mka
```

The filename timestamp is UTC and marks the segment start. `ffmpeg` is run in a supervised loop with reconnect flags and wall-clock segmenting, so if the stream drops the lab command waits briefly and starts recording again. Override the cache root with `--cache-dir` / `TR1_CACHE_DIR`, the chunk size with `--segment-duration` / `TR1_RECORD_SEGMENT_DURATION`, and restart backoff with `--restart-delay` / `TR1_RECORD_RESTART_DELAY`.

`record transcribe` accepts either a recording path or a station alias; for a station alias it uses the latest cached chunk for that station. Transcription results are cached under the same cache root with a versioned layout:

```text
~/.cache/tr1/transcripts/transcribe-v1/bbc/2026/05/18/bbc-20260518t120000z/cpu/tiny/english.json
```

The cached JSON wraps the Whisper output with the recording path, UTC segment start, backend, model, language, and cache version. Pass `--force` to recompute a cached transcript.

## Codex worktrees

Codex worktrees are normal Git worktrees, so ignored files like `.env` and generated `assets/*.mp3` do not move with a thread. The checked-in Codex local environment config at `.codex/environments/environment.toml` copies `.env` and generated media from `assets/` into new worktrees. It finds the main checkout through Git's common directory, so it does not depend on a hard-coded username or checkout path.

Run the default live stream with a specific model:

```sh
go run ./cmd/tr1 --model base
```

Select the backend explicitly:

```sh
go run ./cmd/tr1 --backend cpu --model base
go run ./cmd/tr1 --backend mlx --model medium
```

`--backend auto` is the default. On Apple Silicon with `uv` available it uses MLX; otherwise it falls back to the CPU/OpenAI Whisper backend. The MLX Python runtime is provisioned on first use from the embedded `internal/tr1/mlx.pyproject.toml` into `.tr1/mlx` with `uv sync`, so an installed `tr1` binary does not need the repo checkout to find the Python project metadata.

Tune live latency with the rolling window and step size:

```sh
go run ./cmd/tr1 --model tiny --window 6 --step 1
```

`--window` controls how much recent audio Whisper sees per request, `--step` controls how often new audio is submitted, and `--holdback` keeps words near the unstable end of a window from being printed too early. By default the live worker uses the Python interpreter from the `whisper` CLI shebang; override it with `--python-bin` or `TR1_PYTHON_BIN` if needed.

Play the same decoded stream through system audio while transcribing:

```sh
go run ./cmd/tr1 --play
```

`--play` requires `ffplay`; override it with `--ffplay-bin` or `TR1_FFPLAY_BIN` if needed. You can also set `TR1_PLAY=1`.

Pass `--verbose` or set `TR1_VERBOSE=1` to print diagnostic status messages to stderr.

## Notes

The API key is read from `ELEVENLABS_API_KEY` and is never written by the app. Whisper model files are stored under `.tr1/models`, which is ignored by git.

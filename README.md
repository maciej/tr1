# tr1

Terminal radio receiver and Whisper transcription loop.

## Run

```sh
go run ./cmd/tr1
```

Stop with `Ctrl+C`.

The command resolves the TOK FM playlist at `http://www.tuba.fm/stream.pls?radio=10&mp3=1`, pipes raw 16 kHz PCM from `ffmpeg`, and feeds rolling audio windows into a persistent local Whisper worker. By default only transcript words are printed, streaming to stdout as stable word timestamps come back from Whisper.

TokFM is the default station. Pick another station with a short alias:

```sh
go run ./cmd/tr1 rmf
go run ./cmd/tr1 stream zet
go run ./cmd/tr1 --station trojka
```

Supported stations:

| Canonical name | Convenient aliases |
| --- | --- |
| TokFM | `tokfm`, `tok`, `tok-fm` |
| Polskie Radio Jedynka | `jedynka`, `pr1`, `1` |
| Program Drugi Polskiego Radia | `dwojka`, `dwójka`, `pr2`, `2` |
| Trójka | `trojka`, `trójka`, `pr3`, `3` |
| RMF FM | `rmf`, `rmffm`, `rmf-fm` |
| Radio ZET | `zet`, `radiozet`, `radio-zet` |

You can also set `TR1_STATION`, or pass `--stream-url` / `TR1_STREAM_URL` to use a custom stream URL directly.

## Requirements

- Go 1.26+
- `ffmpeg`
- `openai-whisper` CLI available as `whisper`
- `sag` only for generating the benchmark preview audio

On this machine, `ffmpeg`, `whisper`, and `sag` are installed through Homebrew.

## Useful commands

Generate the synthetic news-broadcast preview audio with ElevenLabs through `sag`:

```sh
ELEVENLABS_API_KEY=... go run ./cmd/tr1 preview
```

If the ElevenLabs key is unavailable, create a local benchmark fixture with macOS speech synthesis:

```sh
go run ./cmd/tr1 preview-local
```

Benchmark Whisper models against that generated preview:

```sh
go run ./cmd/tr1 benchmark --models tiny,base
```

Run the default live stream with a specific model:

```sh
go run ./cmd/tr1 --model base
```

Tune live latency with the rolling window and step size:

```sh
go run ./cmd/tr1 --model tiny --window 6 --step 1
```

`--window` controls how much recent audio Whisper sees per request, `--step` controls how often new audio is submitted, and `--holdback` keeps words near the unstable end of a window from being printed too early. By default the live worker uses the Python interpreter from the `whisper` CLI shebang; override it with `--python-bin` or `TR1_PYTHON_BIN` if needed.

Pass `--verbose` or set `TR1_VERBOSE=1` to print diagnostic status messages to stderr.

## Notes

The API key is read from `ELEVENLABS_API_KEY` and is never written by the app. Whisper model files are stored under `.tr1/models`, which is ignored by git.

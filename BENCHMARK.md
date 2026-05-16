# Benchmark

Fixtures live in `fixtures/*.txt`; generated audio lives in `assets/*.mp3`. Select one with `--fixture`.

| Fixture | Text | Default audio |
| --- | --- | --- |
| `news-preview` | `fixtures/news-preview.txt` | `assets/news-preview.mp3` |
| `biebrza-broadcast` | `fixtures/biebrza-broadcast.txt` | `assets/biebrza-broadcast.mp3` |

Fixture: `assets/news-preview.mp3`, generated locally with `go run ./cmd/tr1 preview-local` because the supplied short-lived ElevenLabs key returned `401 Unauthorized` when `sag` tried to call ElevenLabs.

Audio duration:

```text
20.523s
```

Commands:

```sh
go run ./cmd/tr1 benchmark --backend cpu --models tiny,base,small,medium --verbose
go run ./cmd/tr1 benchmark --backend mlx --models tiny,base,small,medium --verbose
```

Results below are cached steady-state runs. First runs can be slower because OpenAI Whisper downloads `.pt` files and MLX downloads Hugging Face model files.

| Backend | Model | WER | Words | Time | RTF | Throughput |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| cpu | tiny | 0.114 | 44 | 3.696s | 0.180x | 5.55x realtime |
| cpu | base | 0.114 | 46 | 22.720s | 1.107x | 0.90x realtime |
| cpu | small | 0.045 | 43 | 14.159s | 0.690x | 1.45x realtime |
| cpu | medium | 0.000 | 44 | 38.904s | 1.896x | 0.53x realtime |
| mlx | tiny | 0.182 | 45 | 1.856s | 0.090x | 11.06x realtime |
| mlx | base | unstable | 44-267 | 4.828-6.617s | 0.235-0.322x | 3.10-4.25x realtime |
| mlx | small | 0.045 | 43 | 2.869s | 0.140x | 7.15x realtime |
| mlx | medium | 0.000 | 44 | 5.227s | 0.255x | 3.93x realtime |

RTF is wall-clock transcription time divided by audio duration, so lower is better. Throughput is the inverse: audio seconds processed per wall-clock second.

## Notes

- `--backend auto` selects MLX on Apple Silicon when `uv` is available; otherwise it falls back to the CPU/OpenAI Whisper backend.
- The CPU backend still uses the Homebrew `openai-whisper` CLI and stores model files in `.tr1/models`.
- The MLX backend uses an embedded `pyproject.toml` from `cmd/tr1/mlx.pyproject.toml`. On first MLX use, `tr1` writes it to `.tr1/mlx/pyproject.toml`, runs `uv sync --project .tr1/mlx`, and then runs the worker with `.tr1/mlx/.venv/bin/python`.
- MLX model names like `tiny`, `base`, `small`, and `medium` are mapped to Hugging Face repos like `mlx-community/whisper-medium-mlx`. Passing a full repo name also works.
- MLX `base` was rechecked after all model files were cached. It remained slower than MLX `small` and was unstable on this fixture, sometimes decoding a long hallucinated tail. This is not model download time.

For this synthetic Polish fixture, `medium` is the only model that reaches perfect WER on both backends. MLX `medium` is fast enough for live use here; CPU `medium` is not.

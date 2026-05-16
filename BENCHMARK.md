# Benchmark

Fixtures live in `fixtures/*.txt`; generated audio lives in `assets/*.mp3`. The benchmark fixture is intentionally long enough to expose both WER and throughput behavior.

| Fixture | Text | Audio | Duration | Reference words |
| --- | --- | --- | ---: | ---: |
| `biebrza-broadcast` | `fixtures/biebrza-broadcast.txt` | `assets/biebrza-broadcast.mp3` | 295.219s | 554 |

Results below were run on 2026-05-16 with cached model files.

RTF is wall-clock transcription time divided by audio duration, so lower is better. Throughput is the inverse: audio seconds processed per wall-clock second.

## Whole-Clip Throughput and WER

This matrix transcribes the whole fixture once per model. It is the cleanest backend/model throughput comparison.

Commands:

```sh
go run ./cmd/tr1 benchmark --fixture biebrza-broadcast --backend cpu --models tiny,base,small,medium,large --mode whole
go run ./cmd/tr1 benchmark --fixture biebrza-broadcast --backend mlx --models tiny,base,small,medium,large --mode whole
```

| Backend | Model | WER | Words | Time | RTF | Throughput |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| cpu | tiny | 0.137 | 540 | 24.276s | 0.082x | 12.20x realtime |
| cpu | base | 0.052 | 548 | 39.650s | 0.134x | 7.46x realtime |
| cpu | small | 0.022 | 555 | 2m1.141s | 0.410x | 2.44x realtime |
| cpu | medium | 0.013 | 556 | 6m12.273s | 1.261x | 0.79x realtime |
| cpu | large | 0.007 | 558 | 11m8.124s | 2.263x | 0.44x realtime |
| mlx | tiny | 0.153 | 540 | 4.982s | 0.017x | 58.82x realtime |
| mlx | base | 0.081 | 547 | 5.974s | 0.020x | 50.00x realtime |
| mlx | small | 0.025 | 555 | 12.855s | 0.044x | 22.73x realtime |
| mlx | medium | 0.014 | 555 | 30.511s | 0.103x | 9.71x realtime |
| mlx | large | 0.016 | 563 | 1m26.620s | 0.293x | 3.41x realtime |

## MLX Whole vs Chunked

This matrix isolates the WER lift from the previous live settings: 12s rolling window, 3s step, 1500ms holdback.

Commands:

```sh
go run ./cmd/tr1 benchmark --fixture biebrza-broadcast --backend mlx --models tiny,base,small,medium,large --mode whole
go run ./cmd/tr1 benchmark --fixture biebrza-broadcast --backend mlx --models tiny,base,small,medium,large --mode chunked --window 12 --step 3 --holdback 1500ms
```

| Model | Whole WER | Chunked WER | WER lift | Chunked words | Chunked time | Chunked RTF | Chunked throughput |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| tiny | 0.153 | 0.142 | -0.011 | 545 | 15.632s | 0.053x | 18.87x realtime |
| base | 0.081 | 0.094 | +0.013 | 539 | 25.014s | 0.085x | 11.76x realtime |
| small | 0.025 | 0.099 | +0.074 | 555 | 1m7.999s | 0.230x | 4.35x realtime |
| medium | 0.014 | 0.063 | +0.049 | 544 | 4m21.717s | 0.887x | 1.13x realtime |
| large | 0.016 | 0.043 | +0.027 | 543 | 5m29.492s | 1.116x | 0.90x realtime |

## MLX Medium Window Tuning

Whole-clip MLX `medium` remains the target: WER 0.014, 30.511s, 0.103x RTF.

These runs keep the model fixed at MLX `medium` and tune only the chunked window, step, and holdback settings.

| Window | Step | Holdback | WER | WER lift vs whole | Words | Time | RTF | Throughput |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 12s | 3s | 1.5s | 0.063 | +0.049 | 544 | 4m21.717s | 0.887x | 1.13x realtime |
| 20s | 10s | 1.5s | 0.032 | +0.018 | 546 | 1m36.161s | 0.326x | 3.07x realtime |
| 24s | 6s | 1s | 0.032 | +0.018 | 546 | 2m28.347s | 0.502x | 1.99x realtime |
| 24s | 10s | 1s | 0.029 | +0.015 | 548 | 1m27.492s | 0.296x | 3.38x realtime |
| 24s | 12s | 0.5s | 0.040 | +0.026 | 548 | 1m13.047s | 0.247x | 4.05x realtime |
| 24s | 12s | 1s | 0.018 | +0.004 | 553 | 1m8.750s | 0.233x | 4.29x realtime |
| 24s | 12s | 1.5s | 0.020 | +0.006 | 552 | 1m19.717s | 0.270x | 3.70x realtime |
| 24s | 15s | 1s | 0.027 | +0.013 | 548 | 54.145s | 0.183x | 5.46x realtime |
| 27s | 12s | 1s | 0.022 | +0.008 | 554 | 1m46.666s | 0.361x | 2.77x realtime |
| 30s | 5s | 1.5s | 0.027 | +0.013 | 552 | 3m35.005s | 0.728x | 1.37x realtime |
| 30s | 5s | 3s | 0.029 | +0.015 | 548 | 3m34.573s | 0.727x | 1.38x realtime |
| 30s | 10s | 1.5s | 0.023 | +0.009 | 553 | 1m47.350s | 0.364x | 2.75x realtime |
| 30s | 10s | 3s | 0.031 | +0.017 | 548 | 1m52.140s | 0.380x | 2.63x realtime |
| 30s | 10s | 5s | 0.041 | +0.027 | 546 | 2m6.253s | 0.428x | 2.34x realtime |
| 30s | 15s | 1.5s | 0.027 | +0.013 | 550 | 1m13.689s | 0.250x | 4.00x realtime |
| 30s | 20s | 1.5s | 0.029 | +0.015 | 552 | 1m2.635s | 0.212x | 4.72x realtime |

Best measured accuracy is 24s window, 12s step, 1s holdback: WER 0.018, only +0.004 above whole-clip WER while still running 4.29x realtime. These are now the default live and chunked benchmark settings.

The shortest low-WER setting in this sweep is 24s window, 15s step, 1s holdback: WER 0.027 at 5.46x realtime. It is faster and emits less often, but not as close to whole-clip accuracy.

## Notes

- `--backend auto` selects MLX on Apple Silicon when `uv` is available; otherwise it falls back to the CPU/OpenAI Whisper backend.
- The CPU backend uses the Homebrew `openai-whisper` CLI and stores model files in `.tr1/models`.
- The MLX backend uses the embedded `cmd/tr1/mlx.pyproject.toml`. On first MLX use, `tr1` writes it to `.tr1/mlx/pyproject.toml`, runs `uv sync --project .tr1/mlx`, and then runs the worker with `.tr1/mlx/.venv/bin/python`.
- MLX model names like `tiny`, `base`, `small`, `medium`, and `large` map to Hugging Face repos like `mlx-community/whisper-medium-mlx`. Passing a full repo name also works.
- Chunked benchmark time includes repeated overlapping-window inference, so it measures live-mode cost rather than model-only whole-clip speed.
- OpenAI Whisper's README says `transcribe()` processes audio with a sliding 30-second window: <https://github.com/openai/whisper/blob/main/README.md>.
- The local MLX Whisper source uses `CHUNK_LENGTH = 30` and `N_FRAMES = 3000` in `mlx_whisper/audio.py`, and its long-form transcription loop advances through those 30s windows in `mlx_whisper/transcribe.py`.
- Whisper-Streaming uses a local-agreement policy with self-adaptive latency for streaming Whisper: <https://github.com/ufal/whisper_streaming> and <https://arxiv.org/abs/2307.14743>.
- Simul-Whisper identifies truncated words at chunk boundaries as a primary streaming failure mode: <https://arxiv.org/abs/2406.10052>.

On this fixture, MLX `medium` is the best whole-clip tradeoff: near-CPU-medium WER at 9.71x realtime. With the current live windowing settings, MLX `medium` stays barely faster than realtime but pays a noticeable WER penalty; MLX `large` improves chunked WER but falls below realtime.

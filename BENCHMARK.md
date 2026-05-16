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

This matrix isolates the WER lift from the selected live settings: 12s rolling window, 3s step, 1500ms holdback.

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

## Notes

- `--backend auto` selects MLX on Apple Silicon when `uv` is available; otherwise it falls back to the CPU/OpenAI Whisper backend.
- The CPU backend uses the Homebrew `openai-whisper` CLI and stores model files in `.tr1/models`.
- The MLX backend uses the embedded `cmd/tr1/mlx.pyproject.toml`. On first MLX use, `tr1` writes it to `.tr1/mlx/pyproject.toml`, runs `uv sync --project .tr1/mlx`, and then runs the worker with `.tr1/mlx/.venv/bin/python`.
- MLX model names like `tiny`, `base`, `small`, `medium`, and `large` map to Hugging Face repos like `mlx-community/whisper-medium-mlx`. Passing a full repo name also works.
- Chunked benchmark time includes repeated overlapping-window inference, so it measures live-mode cost rather than model-only whole-clip speed.

On this fixture, MLX `medium` is the best whole-clip tradeoff: near-CPU-medium WER at 9.71x realtime. With the current live windowing settings, MLX `medium` stays barely faster than realtime but pays a noticeable WER penalty; MLX `large` improves chunked WER but falls below realtime.

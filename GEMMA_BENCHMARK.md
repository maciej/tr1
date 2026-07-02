# Gemma 4 12B ASR Benchmark

## Conclusion

Gemma 4 12B is not a viable replacement for Whisper in the current `tr1` Polish radio transcription pipeline.

The model and runtimes can ingest audio, and direct audio transcription works for short English, German, and French synthetic clips. Polish ASR failed across Ollama, direct llama.cpp, a real Biebrza fixture, and short ElevenLabs-generated Polish clips. The failures were not just formatting errors: the model repeatedly heard Polish as other Slavic/Cyrillic-language audio, returned empty output, or looped on Cyrillic text. Whisper MLX medium could transcribe the same short Polish audio mostly correctly, so the fixture audio was intelligible.

Keep Whisper/MLX as the audio transcription front end for `tr1`.

## Scope

This benchmark covered the Gemma 4 12B family only:

- `gemma4:12b`
- `gemma4:12b-mlx` as a text-only/runtime note, not as an audio substitute
- Official `google/gemma-4-12B-it` documentation and model metadata

The benchmark did not use smaller `gemma4:e2b` or `gemma4:e4b` variants.

## Local Artifacts

Ollama model:

```sh
gemma4:12b
```

Ollama-reported model metadata:

```text
architecture: gemma4
parameters: 11.9B
context length: 131072
quantization: Q4_K_M
capabilities: completion, vision, audio, tools, thinking
projector: BF16 mmproj-gemma-4-12B-it
```

Key GGUF blobs used for direct llama.cpp tests:

```text
~/.ollama/models/blobs/sha256-5cf8a1f2fc4268b3fd628743675910cf1d8137c4742d0be401c3e885f605023a
~/.ollama/models/blobs/sha256-a18399bf318a19035443d435e6fa1ac7b3aefb948116c59baac790df499edf69
```

Temporary benchmark audio was generated under:

```text
/tmp/gemma-hf-fixtures
```

## Runtime Findings

### Ollama

Homebrew Ollama `0.30.2` and official Ollama `0.30.3` could not pull `gemma4:12b`; the model manifest requires Ollama `0.30.4`. Official Ollama `v0.30.4-rc0` could pull and run the model.

Working audio routes:

- CLI: `ollama run gemma4:12b /path/to.wav ...`
- Native REST: `/api/chat` with base64 WAV bytes in `messages[].images`
- Native REST: `/api/generate` with base64 WAV bytes in top-level `images`
- OpenAI-compatible REST: `/v1/chat/completions` with `input_audio`

Non-working or poor routes:

- A guessed native `messages[].audio` field was ignored.
- `/v1/audio/transcriptions` accepted requests but overgenerated or hung in local tests.

For OpenAI-compatible Ollama chat, these fields were needed to avoid hidden thinking consuming the output budget:

```json
{
  "reasoning_effort": "none",
  "think": false
}
```

### llama.cpp

Ollama `v0.30.4-rc0` bumps llama.cpp from `b9479` to `b9493`; the important upstream change is Gemma 4 unified audio support. Direct tests used llama.cpp `b9495` because its macOS arm64 release includes `llama-server` and `llama-mtmd-cli`.

`llama-mtmd-cli` worked mechanically but was awkward for ASR because it emitted or consumed budget in Gemma thinking channels.

`llama-server` was the cleanest direct harness:

```sh
DYLD_LIBRARY_PATH=.tr1/llama-b9495/llama-b9495 \
.tr1/llama-b9495/llama-b9495/llama-server \
  -m ~/.ollama/models/blobs/sha256-5cf8a1f2fc4268b3fd628743675910cf1d8137c4742d0be401c3e885f605023a \
  --mmproj ~/.ollama/models/blobs/sha256-a18399bf318a19035443d435e6fa1ac7b3aefb948116c59baac790df499edf69 \
  --jinja \
  --reasoning off \
  --host 127.0.0.1 \
  --port 18081 \
  -ngl 99 \
  -c 4096 \
  --temp 0 \
  --top-k 1 \
  --top-p 1
```

The server reported `thinking = 0` and accepted OpenAI-compatible `input_audio` requests.

### Hugging Face / Transformers

Google and Hugging Face currently recommend the Transformers any-to-any path for Gemma 4 audio:

```python
from transformers import pipeline

pipe = pipeline("any-to-any", model="google/gemma-4-12B-it")
messages = [{
    "role": "user",
    "content": [
        {"type": "text", "text": "..."},
        {"type": "audio", "audio": "/path/to/audio.wav"},
    ],
}]
outputs = pipe(messages, return_full_text=False, generate_kwargs=gen_kwargs)
```

Local HF access was blocked: the token in `.env` could query `google/gemma-4-12B-it` metadata but was not authorized to download gated model files. `AutoProcessor.from_pretrained("google/gemma-4-12B-it")` failed with HTTP 403 for `config.json`.

This probably would not change the practical conclusion. HF would use official safetensors rather than the local Q4 GGUF, so it could improve quantization-sensitive edge cases, but the direct llama.cpp tests already showed a consistent Polish ASR failure while English, German, and French worked.

## Audio Preparation

Google's Gemma 4 audio guidance and llama.cpp's Gemma 4 audio path both point to:

- mono audio
- 16 kHz sample rate
- clips at or below 30 seconds
- float waveform internally
- about 25 audio tokens per second

For local file tests, WAV was preferred to avoid codec and resampler variance:

```sh
ffmpeg -y -i input.mp3 -af 'apad=pad_dur=0.5' -ar 16000 -ac 1 output.wav
```

Adding at least 0.5 seconds of trailing silence helped short English synthetic clips avoid clipped final words. Loudness normalization was not helpful in these tests and sometimes worsened proper-noun behavior.

## Prompt

The main ASR prompt followed Google's documented format:

```text
Transcribe the following speech segment in {LANGUAGE} into {LANGUAGE} text.
Follow these specific instructions for formatting the answer:
* Only output the transcription, with no newlines.
* When transcribing numbers, write the digits, i.e. write 1.7 and not one point seven, and write 3 instead of three.
```

For Polish, extra instructions such as "Use the Polish Latin alphabet" and "Do not use Cyrillic" did not fix the failures.

## Test Results

### Short Synthetic Clips

English, German, and French clips were generated with `sag` / ElevenLabs `eleven_multilingual_v2`, converted to 16 kHz mono WAV, and sent through direct llama.cpp `llama-server`.

| Language | Reference | Gemma 4 12B output | Result |
| --- | --- | --- | --- |
| English | The meeting starts at 3 PM. Please bring the blue folder and the printed agenda. | The meeting starts at 3:00 p.m. Please bring the blue folder and the printed agenda. | Good |
| German | Der Zug nach Berlin fährt um 8 Uhr 45 ab. Bitte bleiben Sie auf Gleis 3 und achten Sie auf die Durchsage. | Der Zug nach Berlin fährt um 8:45 Uhr ab. Bitte bleiben Sie auf Gleis 3 und achten Sie auf die Durchsage. | Good |
| French | Le musée ouvre à 10 heures. Nous visiterons la galerie principale avant de prendre un café près de la rivière. | Le musée ouvre à 10h, nous visiterons la galerie principale avant de prendre un café près de la rivière. | Good |
| Polish | Spotkanie zaczyna się o 15:00. Proszę przynieść niebieski folder i wydrukowany program. | empty output with the default voice | Failed |
| Polish | Same text with an alternate ElevenLabs voice | Cyrillic/Ukrainian-like text | Failed |

Whisper MLX medium transcribed the alternate Polish clip mostly correctly:

```text
Spotkanie zaczyna się o 15 tekstów. Proszę przynieść niebieski folder i wydrukowany program.
```

That sanity check shows the Polish synthetic audio was intelligible.

### Biebrza Fixture

Existing Polish fixture:

```text
fixtures/biebrza-broadcast.txt
assets/biebrza-broadcast.mp3
```

A 30-second slice was converted to 16 kHz mono WAV and tested through Ollama and direct llama.cpp.

Result:

- Output drifted into Cyrillic Slavic text.
- Longer generations repeated Cyrillic-like fragments.
- Prompts requiring Polish Latin script did not fix the issue.

This was the decisive failure for the `tr1` use case.

## Thinking Budget Experiment

Hypothesis: Polish failures might be caused by hidden reasoning consuming the token budget or by the context window being too small.

Test:

```sh
llama-server \
  --reasoning on \
  --reasoning-budget 256 \
  -c 8192
```

Result:

- The short Polish clip no longer returned empty output.
- The model's reasoning content revealed that it perceived the Polish audio as Macedonian/Bulgarian-like Cyrillic text, then transliterated it into Latin.
- The Biebrza clip was perceived as Russian/Belarusian-like speech and still produced Cyrillic Slavic output.

Conclusion: thinking budget and larger context fix output mechanics, not Polish recognition quality.

## Practical Decision

Do not use Gemma 4 12B direct audio ASR as a replacement for Whisper/MLX in `tr1`.

Use:

```text
Whisper/MLX -> pyannote -> cleanup/speaker-map stage
```

Gemma 4 12B may still be useful as a local text reasoning model for cleanup or structured postprocessing, but this document does not benchmark that path. The audio replacement question is settled for now: Gemma 4 12B is not reliable enough for Polish radio transcription on this host and these runtimes.

## Sources

- Google Gemma 4 model card: https://ai.google.dev/gemma/docs/core/model_card_4
- Google Gemma audio guide: https://ai.google.dev/gemma/docs/capabilities/audio
- Hugging Face any-to-any docs: https://huggingface.co/docs/transformers/tasks/any_to_any
- Hugging Face serving audio docs: https://huggingface.co/docs/transformers/serve-cli/serving
- llama.cpp Gemma 4 audio PR: https://github.com/ggml-org/llama.cpp/pull/21421
- Ollama `v0.30.3...v0.30.4-rc0` compare: https://github.com/ollama/ollama/compare/v0.30.3...v0.30.4-rc0

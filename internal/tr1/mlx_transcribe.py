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

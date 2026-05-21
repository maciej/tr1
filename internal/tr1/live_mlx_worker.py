import base64
import json
import sys
import traceback

import mlx_whisper
import numpy as np

model_name = sys.argv[1]
language = sys.argv[3]

for line in sys.stdin:
    try:
        req = json.loads(line)
        raw = base64.b64decode(req["pcm16_b64"])
        audio = np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0
        result = mlx_whisper.transcribe(
            audio,
            path_or_hf_repo=model_name,
            language=language,
            task="transcribe",
            word_timestamps=True,
            verbose=False,
            condition_on_previous_text=False,
            hallucination_silence_threshold=1.0,
            initial_prompt=req.get("initial_prompt") or None,
        )
        words = []
        for segment in result.get("segments", []):
            for word in segment.get("words") or []:
                words.append(
                    {
                        "word": word.get("word", ""),
                        "start": float(word.get("start", 0.0)),
                        "end": float(word.get("end", 0.0)),
                        "probability": float(word.get("probability", 0.0)),
                    }
                )
        print(
            json.dumps(
                {
                    "seq": req["seq"],
                    "offset": req["offset"],
                    "duration": req["duration"],
                    "text": result.get("text", ""),
                    "words": words,
                },
                ensure_ascii=False,
            ),
            flush=True,
        )
    except Exception as exc:
        print(
            json.dumps(
                {
                    "seq": req.get("seq", 0) if "req" in locals() else 0,
                    "offset": req.get("offset", 0.0) if "req" in locals() else 0.0,
                    "duration": req.get("duration", 0.0) if "req" in locals() else 0.0,
                    "error": str(exc),
                    "text": "",
                    "words": [],
                }
            ),
            flush=True,
        )
        traceback.print_exc(file=sys.stderr)

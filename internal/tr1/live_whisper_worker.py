import base64
import json
import sys
import traceback

import numpy as np
import whisper

model_name = sys.argv[1]
model_dir = sys.argv[2]
language = sys.argv[3]
model = whisper.load_model(model_name, download_root=model_dir)

for line in sys.stdin:
    try:
        req = json.loads(line)
        raw = base64.b64decode(req["pcm16_b64"])
        audio = np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0
        result = model.transcribe(
            audio,
            language=language,
            task="transcribe",
            word_timestamps=True,
            fp16=False,
            verbose=None,
            condition_on_previous_text=False,
            initial_prompt=req.get("initial_prompt") or None,
        )
        words = []
        for segment in result.get("segments", []):
            for word in segment.get("words", []):
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

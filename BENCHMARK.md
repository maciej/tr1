# Benchmark

Fixture: `assets/news-preview.mp3`, generated locally with `go run ./cmd/tr1 preview-local` because the supplied short-lived ElevenLabs key returned `401 Unauthorized` when `sag` tried to call ElevenLabs.

Command:

```sh
go run ./cmd/tr1 benchmark --models tiny,base
```

Results on this machine:

| Model | WER | Words | Time |
| --- | ---: | ---: | ---: |
| tiny | 0.114 | 44 | 6.628s |
| base | 0.136 | 47 | 21.917s |

For this synthetic Polish fixture, `tiny` scored slightly better and was much faster. On live TOK FM speech, `base` is still the default because it is usually less brittle across speakers and background audio.

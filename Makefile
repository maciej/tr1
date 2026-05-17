SHELL := /bin/sh

.PHONY: run preview benchmark

run:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run ./cmd/tr1

preview:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run ./cmd/tr1-lab preview

benchmark:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run ./cmd/tr1-lab benchmark

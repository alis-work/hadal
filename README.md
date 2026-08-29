# Hadal

Hadal is a planned Somali-first translation and transcription platform.

The initial interface will be a WhatsApp bot. It will support Somali or English voice notes with opposite-language text translation, Somali/English text translation, and image-text OCR followed by translation. A React Native + TypeScript mobile app for iOS and Android is planned later.

## Status

The first local end-to-end audio path is available: a Go upload API writes audio to local filesystem storage, PostgreSQL stores job metadata/results, Redis Streams dispatches work, and a dedicated Python worker calls OpenAI audio transcription followed by a separate OpenAI text classification-and-translation request.

## Direction

- Go backend/API and workers, PostgreSQL, Redis, Docker Compose, and OpenAI APIs initially.
- WhatsApp Business Cloud API for messaging.
- Asynchronous processing for media workloads, with clear job state, retries, idempotency, and cost controls.
- Provider abstractions for transcription, translation, and OCR so commercial services can later coexist with a custom Somali ASR model.

Kafka, document handling, conversation mode, history, sharing, and audio responses are not MVP requirements.

## Project Documentation

- [Architecture](docs/architecture.md): boundaries, processing flows, data ownership, and operational requirements.
- [Roadmap](docs/roadmap.md): MVP-first delivery sequence and deferred scope.

## Security

Secrets, WhatsApp tokens, database passwords, API keys, and phone-number allowlists must not be committed. When runtime configuration is introduced, `.env.example` will list variable names without values.

## Local Audio Workflow

1. Create local configuration: `cp .env.example .env`. Set `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `POSTGRES_PORT`. For host processes set `DATABASE_URL=postgres://USER:PASSWORD@localhost:5432/DB?sslmode=disable` and `REDIS_URL=redis://localhost:6379/0`. For Compose migration/API/worker services set `COMPOSE_DATABASE_URL=postgres://USER:PASSWORD@postgres:5432/DB?sslmode=disable` and `COMPOSE_REDIS_URL=redis://redis:6379/0`. URL-encode a password when needed. Set `AUDIO_TEMP_DIR=./data/audio`, `API_ADDR=:8080`, and the ignored `OPENAI_API_KEY`; optionally set `OPENAI_TRANSCRIPTION_MODEL=gpt-transcribe`, `OPENAI_TRANSLATION_MODEL=gpt-4o-mini`, and `PROCESSING_STALE_AFTER_SECONDS=3600` for host execution. The API does not receive the OpenAI key; only the worker does. The worker will not start without the key.
2. Start PostgreSQL and Redis: `docker compose up -d postgres redis`.
3. Apply migrations: `docker compose --profile tools run --rm migrate`.
4. Start the API on the host without exposing the OpenAI key to it: `set -a && source .env && set +a && unset OPENAI_API_KEY && go run ./cmd/api`.
5. In another terminal, install the worker dependencies and start it with the key: `python3 -m venv .venv && .venv/bin/pip install -r worker/requirements.txt && set -a && source .env && set +a && .venv/bin/python -m worker.worker`.
6. Or run both application processes in Compose after migration: `docker compose --profile app up --build api worker`.
7. Upload a Somali or English audio file: `curl -F "file=@/path/to/audio.m4a" http://localhost:8080/api/transcriptions`.
8. Copy the returned `id`, then poll it: `curl http://localhost:8080/api/transcriptions/ID`.
9. A real local smoke test is complete when the response changes from `PENDING` (or `PROCESSING`) to `COMPLETED` and includes detected `language`, `targetLanguage`, source `transcript`, and translated `text`. Somali speech yields English `text`; English speech yields Somali `text`. Each completed job consumes two OpenAI calls: audio transcription and structured text classification/translation.

The API accepts `.mp3`, `.wav`, `.ogg`, `.opus`, `.m4a`, `.mp4`, and `.webm` audio. It returns text only, never generated audio. It does not implement WhatsApp, authorization, quotas, or outbound replies. The `.env` file is ignored. The initial migration contains no phone numbers; add real phone numbers only through a controlled runtime or administrative path.

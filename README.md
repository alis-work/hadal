# Hadal

Hadal is a planned Somali-first translation and transcription platform.

The initial interface will be a WhatsApp bot. It will support Somali voice notes to Somali transcription and English translation, Somali/English text translation, and image-text OCR followed by translation. A React Native + TypeScript mobile app for iOS and Android is planned later.

## Status

Documentation, local PostgreSQL setup, and the initial database schema are in place. No Go application implementation exists yet.

## Direction

- Go backend/API and workers, PostgreSQL, Redis, Docker Compose, and OpenAI APIs initially.
- WhatsApp Business Cloud API for messaging.
- Asynchronous processing for media workloads, with clear job state, retries, idempotency, and cost controls.
- Provider abstractions for transcription, translation, and OCR so commercial services can later coexist with a custom Somali ASR model.

Kafka, Python ML workloads, document handling, conversation mode, history, sharing, and audio responses are not MVP requirements.

## Project Documentation

- [Architecture](docs/architecture.md): boundaries, processing flows, data ownership, and operational requirements.
- [Roadmap](docs/roadmap.md): MVP-first delivery sequence and deferred scope.

## Security

Secrets, WhatsApp tokens, database passwords, API keys, and phone-number allowlists must not be committed. When runtime configuration is introduced, `.env.example` will list variable names without values.

## Local Database

1. Copy `.env.example` to `.env` and set local PostgreSQL credentials. Keep `DATABASE_URL` pointed at `postgres` (the Compose service hostname), not `localhost`; URL-encode its password if necessary.
2. Start PostgreSQL with `docker compose up -d postgres`.
3. Apply tracked migrations with `docker compose --profile tools run --rm migrate`.
4. Connect with `docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'`.

The `.env` file is ignored. Migrations create no allowlisted senders; add real phone numbers only through a controlled runtime or administrative path when that is implemented.

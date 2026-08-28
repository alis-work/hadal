# Hadal

Hadal is a planned Somali-first translation and transcription platform.

The initial interface will be a WhatsApp bot. It will support Somali voice notes to Somali transcription and English translation, Somali/English text translation, and image-text OCR followed by translation. A React Native + TypeScript mobile app for iOS and Android is planned later.

## Status

Documentation and architecture planning only. No application implementation exists yet.

## Direction

- Go backend/API and workers, PostgreSQL, Redis, Docker Compose, and OpenAI APIs initially.
- WhatsApp Business Cloud API for messaging.
- Asynchronous processing for media workloads, with clear job state, retries, idempotency, and cost controls.
- Provider abstractions for transcription, translation, and OCR so commercial services can later coexist with a custom Somali ASR model.

Kafka, k3s/Kubernetes, Raspberry Pi hosting, Python ML workloads, document handling, conversation mode, history, sharing, and audio responses are not MVP requirements.

## Project Documentation

- [Architecture](docs/architecture.md): boundaries, processing flows, data ownership, and operational requirements.
- [Roadmap](docs/roadmap.md): MVP-first delivery sequence and deferred scope.

## Security

Secrets, WhatsApp tokens, database passwords, API keys, and phone-number allowlists must not be committed. When runtime configuration is introduced, `.env.example` will list variable names without values.

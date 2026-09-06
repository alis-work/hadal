# Hadal

Hadal is a planned Somali-first translation and transcription platform.

The initial interface will be a WhatsApp bot. It will support Somali or English voice notes with opposite-language text translation, Somali/English text translation, and image-text OCR followed by translation. A React Native + TypeScript mobile app for iOS and Android is planned later.

## Status

The first local end-to-end audio path is available: a Go upload API writes audio to local filesystem storage, PostgreSQL stores job metadata/results, Redis Streams dispatches work, and a dedicated Python worker calls OpenAI transcription, translation, and translation validation.

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

1. Create local configuration: `cp .env.example .env`. Set `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `POSTGRES_PORT`. For host processes set `DATABASE_URL=postgres://USER:PASSWORD@localhost:5432/DB?sslmode=disable` and `REDIS_URL=redis://localhost:6379/0`. For Compose services set `COMPOSE_DATABASE_URL=postgres://USER:PASSWORD@postgres:5432/DB?sslmode=disable` and `COMPOSE_REDIS_URL=redis://redis:6379/0`. URL-encode passwords when needed. Set `AUDIO_TEMP_DIR=./data/audio`, `API_ADDR=:8080`, `GLOBAL_DAILY_AUDIO_LIMIT=100`, all WhatsApp variables described below, the two ignored registration-code variables, and the ignored `OPENAI_API_KEY`. Optional worker values include `OPENAI_TRANSCRIPTION_MODEL=gpt-transcribe`, `OPENAI_TRANSLATION_MODEL=gpt-4o-mini`, `PROVIDER_MAX_ATTEMPTS=3`, and `PROCESSING_STALE_AFTER_SECONDS=3600`. The API receives Meta credentials but not the OpenAI key; only the worker receives the OpenAI key.
2. Start PostgreSQL and Redis: `docker compose up -d postgres redis`.
3. Apply migrations: `docker compose --profile tools run --rm migrate`.
4. Start the API on the host without exposing the OpenAI key to it: `set -a && source .env && set +a && unset OPENAI_API_KEY && go run ./cmd/api`.
5. In another terminal, install the worker dependencies and start it with the key: `python3 -m venv .venv && .venv/bin/pip install -r worker/requirements.txt && set -a && source .env && set +a && .venv/bin/python -m worker.worker`.
6. Or run both application processes in Compose after migration: `docker compose --profile app up --build api worker`.
7. Register a local development sender with one runtime registration code: `curl -X POST -H "X-Hadal-Sender: +15550000001" -H "Content-Type: application/json" -d '{"code":"YOUR_RUNTIME_CODE"}' http://localhost:8080/api/registrations`.
8. Upload a Somali or English audio file with the same sender: `curl -H "X-Hadal-Sender: +15550000001" -F "file=@/path/to/audio.m4a" http://localhost:8080/api/transcriptions`.
9. Copy the returned `id`, then poll it: `curl http://localhost:8080/api/transcriptions/ID`.
10. A real local smoke test is complete when the response changes from `PENDING` (or `PROCESSING`) to `COMPLETED` and includes detected `language`, `targetLanguage`, source `transcript`, and translated `text`. Somali speech yields English `text`; English speech yields Somali `text`. Each completed job consumes three OpenAI calls: audio transcription, structured text classification/translation, and translation validation.

The API accepts `.mp3`, `.wav`, `.ogg`, `.opus`, `.m4a`, `.mp4`, and `.webm` audio. It requires `ffprobe` on a host installation; Compose is recommended because the API image includes ffmpeg. It returns text only, never generated audio. `X-Hadal-Sender` remains a development-only identity shim for the local upload endpoint. WhatsApp intake uses the signed webhook sender. Uploads are limited to three requests per sender per minute. Permitted users can submit 10 accepted messages per day of up to 30 seconds each. Recruiters can submit three accepted messages total of up to 10 seconds each and are disabled after the third. A configurable global daily ceiling protects total paid processing. Rejected duration, quota, and rate-limit checks do not create jobs. The `.env` file and local `audio/` directory are ignored.

## WhatsApp Webhook Verification

Expose the local API through a public HTTPS tunnel, then use `https://YOUR_PUBLIC_HOST/webhook` as the Meta callback URL. Set the same independently generated value for `WHATSAPP_VERIFY_TOKEN` locally and in Meta's Verify token field. This value is not a Meta access token. Meta verifies the endpoint with a `GET` request and receives its `hub.challenge` unchanged.

The API also requires `WHATSAPP_APP_SECRET` from **App settings > Basic**, a fresh `WHATSAPP_ACCESS_TOKEN` with `whatsapp_business_messaging`, `WHATSAPP_PHONE_NUMBER_ID` from the WhatsApp API setup request URL, and `WHATSAPP_GRAPH_API_VERSION` (currently `v26.0`). Never paste these values into documentation, screenshots, or Git. Graph API Explorer tokens are temporary; use a properly scoped system-user token for production.

`POST /webhook` validates Meta's `X-Hub-Signature-256`, persists provider message IDs idempotently, and returns immediately. A durable Go intake loop registers senders whose text is exactly one configured four-digit code, or authorizes and downloads voice media before queueing the existing worker. Registration codes are classified during authenticated intake and never stored. The Python worker persists the transcript and opposite-language translation, creates an outbox reply transactionally, and retries transient provider failures. A Go dispatcher sends pending replies and records Meta's provider message ID.

While the Meta app is unpublished, Meta only delivers dashboard-generated test webhooks. Use **Test** beside the subscribed `messages` webhook field to verify intake. Real user messages require the app to be published.

## Development Registration

`PERMITTED_USER_REGISTRATION_CODE` and `RECRUITER_REGISTRATION_CODE` are separate four-digit runtime secrets. The local endpoint binds them to `X-Hadal-Sender`; WhatsApp users send exactly the code in a text message and are bound to Meta's signed sender identity. A recruiter, including one disabled after three messages, may redeem the permitted-user code to become an active permitted user. A permitted user cannot be downgraded to recruiter.

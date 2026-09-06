# Roadmap

This is an MVP-first implementation plan. Each phase should be completed and verified before expanding scope. Recommendations are labeled as such; unselected providers, storage, queue technology, and deployment details remain open.

## MVP

### 1. Foundation

- Create the Go modular-monolith structure and local Docker Compose development environment.
- Add PostgreSQL for durable application, job, and result metadata. Initial local Compose configuration and allowlist/daily-quota schema are complete.
- Add environment-based configuration and `.env.example` with variable names only. The initial PostgreSQL template is complete.
- Establish structured logging, metrics, and tracing foundations.
- Define configuration and secret-handling boundaries without committing credentials or allowlists.

### 2. WhatsApp Intake and Protection

- Integrate the official WhatsApp Business Cloud API webhook, including verification and inbound-message handling. Signature verification, durable message intake, and voice-message dispatch are complete locally.
- Bind four-digit registration-code redemption to the verified WhatsApp sender. WhatsApp registration is complete locally and classifies codes before persistence; the development upload API retains the temporary `X-Hadal-Sender` E.164 shim.
- Implement allowlisted sender authorization before paid work.
- Enforce permitted-user 30-second/10-per-day policy and recruiter 10-second/three-lifetime-then-disabled policy atomically before paid work.
- Persist inbound-message identifiers and use them to make webhook handling idempotent. Complete locally.
- Send basic non-media replies through WhatsApp to validate the integration boundary. The durable outbox and Graph API adapter are complete; a live credential test remains.

### 3. Text Translation

- Define a translation-provider interface. Complete locally.
- Add the initial OpenAI-backed translation adapter. Complete locally.
- Support Somali-to-English and English-to-Somali WhatsApp text workflows. Complete locally with durable asynchronous processing.
- Persist translation outcomes and provider-use metadata needed for support and safeguards.
- Verify the full inbound text to outbound reply path.

### 4. Asynchronous Audio Workflow

- Choose and add object/file storage for WhatsApp media; do not store media bytes in PostgreSQL.
- Choose and add a durable job mechanism appropriate to the initial 5-10-user scale.
- Define job and event states, retry rules, terminal failures, and idempotency behavior.
- Define a transcription-provider interface and add the initial OpenAI audio-transcription adapter.
- Implement OpenAI audio auto-detection without a language hint, preserve the source transcript, and use separate strict structured OpenAI calls to classify/translate Somali/English into the opposite language and sanity-check the proposed translation before the WhatsApp text reply.
- Add appropriate media deduplication and ensure webhook requests do not wait for processing.

### 5. Image OCR Workflow

- Select an OCR provider and add it behind an OCR-provider interface.
- Implement image storage, asynchronous OCR, translation into the other language, result persistence, and WhatsApp reply.
- Apply the same authorization, quota, rate-limit, idempotency, retry, and cost controls as audio processing.

### 6. MVP Hardening

- Exercise retry, duplicate-message, failed-provider, failed-media, and failed-reply paths.
- Add focused tests for authorization, safeguards, job transitions, and provider adapters.
- Document local setup and the exact verification commands after tooling exists.
- Retain transcript corrections in a form usable for future evaluation/training work.
- Version Dockerfiles, publish the application image to Docker Hub, and deploy it to the Ubuntu Raspberry Pi through k3s.
- Keep Kubernetes Secret values in an ignored runtime secret file or other secrets-management mechanism; version only manifests that reference those Secrets.

## Later Features

- React Native and TypeScript mobile client for iOS and Android.
- Documents and PDFs.
- Conversation mode, history, sharing, and audio responses.
- User-facing transcript corrections and associated evaluation workflows.
- A custom Python ASR worker using PyTorch, Hugging Face, or a Somali-specific model, connected through the existing transcription-provider boundary.
- Evaluation of commercial and custom ASR quality for Somali audio.

## Optional Infrastructure

- Kafka for media-processing events when the chosen durable-job mechanism no longer meets reliability, throughput, integration, or operational needs.

## Recommendations

- Start with the smallest durable asynchronous design that satisfies media retries and idempotency for 5-10 users; do not add Kafka simply to demonstrate event-driven architecture.
- Keep Go workflow logic provider-neutral so OpenAI can be replaced without rewriting user flows.
- Treat media, provider calls, and outbound WhatsApp delivery as separately observable operations.
- Define retention, deletion, and correction-consent policies before collecting significant user media or correction data.

These recommendations are not confirmed requirements.

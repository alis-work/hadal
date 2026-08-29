# Architecture

## Purpose and Scope

Hadal is a Somali-first translation and transcription platform. The first client is a WhatsApp Business Cloud API bot; a React Native and TypeScript mobile client is a later interface.

The MVP user flows are:

- Somali or English voice or audio to an OpenAI auto-detected source transcript and text translation into the other language.
- Somali text to English text.
- English text to Somali text.
- A photo containing Somali or English text to OCR and translation into the other language.

Documents/PDFs, conversation mode, history, sharing, and audio responses are later features.

## Confirmed Decisions

- Go is the primary language for the API and workers.
- PostgreSQL stores application metadata and durable results. It must not store uploaded media bytes.
- Redis supports rate limiting, quotas, and other short-lived coordination or cache needs when introduced.
- Docker and Docker Compose are the local-development direction.
- The initial transcription and translation paths use separate OpenAI API calls. OpenAI transcription auto-detects the audio language; a structured OpenAI translation response identifies Somali (`so`) or English (`en`) and translates to the opposite language. OCR uses a provider behind an abstraction.
- WhatsApp uses the official WhatsApp Business Cloud API.
- The Python audio queue worker is separate from the Go application. It submits audio to OpenAI transcription without a language hint, then submits the source transcript in a separate OpenAI text request. The structured translation response determines Somali (`so`) or English (`en`) and the opposite target language; unsupported languages fail. The worker remains behind the transcription boundary so a custom Somali ASR model can replace it.
- The system starts as a modular monolith. Workers are separate processes only when asynchronous or independent scaling warrants them.
- Media processing is asynchronous. Webhook HTTP requests must not wait for transcription or OCR to complete.
- All paid processing is limited to allowlisted WhatsApp phone numbers and protected by per-user quotas, rate limits, and global cost safeguards.
- Provider interfaces are required for transcription, translation, and OCR so providers can be replaced and a custom Somali ASR provider can be added.
- User transcript corrections are retained as potential future evaluation or training data.
- Structured logging, metrics, and tracing are required operational capabilities.

## System Boundaries

### Go Application

The Go application owns the WhatsApp webhook, sender authorization, synchronous text translation requests, job creation, persistence, provider selection, and outbound WhatsApp replies. Its internal modules should separate transport, application workflow, domain/persistence, and provider adapters without introducing networked services merely for separation.

### Workers

Workers execute durable, long-running media steps outside the webhook request path. Audio transcription, image OCR, and downstream translation are worker responsibilities. Workers persist state transitions and results, then request the Go application's outbound-message capability or otherwise use the same integration boundary.

### Storage

- PostgreSQL: users or sender records, authorization and quota metadata, job metadata and state, provider request/result metadata, transcripts, translations, corrections, and delivery outcomes.
- Object or file storage: downloaded WhatsApp media and any derived media artifacts. The initial audio path uses a configured filesystem directory; PostgreSQL stores only its path and metadata.
- Redis: enforcement and coordination data with appropriate expiry; it is not the source of truth for durable job state.

The initial schema contains `whatsapp_senders` for normalized sender-phone allowlist policy, a separate international country code, administrator designation, and daily request limits; `NULL` means no per-sender daily limit. `daily_quota_usage` stores durable per-sender daily paid-request reservations and completions. No allowlisted phone numbers are stored in versioned SQL. A future application transaction must make a quota reservation atomically before a paid provider call and adjust it after the outcome; Redis may reject short bursts but must not be the sole daily-quota authority.

Registration codes are four-digit environment secrets, not database values. Before WhatsApp integration, the required `X-Hadal-Sender` E.164 header is a development-only identity shim; verified webhook identity replaces it. A `PERMITTED_USER` has a 30-second maximum audio length and 10 accepted audio messages per day. A `RECRUITER` has a 10-second maximum length and three accepted audio messages over its lifetime; accepting the third atomically transitions the sender to `DISABLED`. Redis rejects bursts above three submissions per sender per minute before storage; PostgreSQL atomically enforces durable quotas and recruiter state after ffprobe duration validation and before queueing.

### External Providers

- WhatsApp Business Cloud API: webhook delivery, inbound-message metadata/media access, and outbound replies.
- Transcription provider: OpenAI initially; custom Somali ASR later.
- Translation and language-classification provider: OpenAI initially.
- OCR provider: to be selected.

Provider adapters should normalize provider-specific requests, responses, failures, and usage metadata behind application-facing interfaces. This keeps workflow code independent of a specific vendor and makes cost controls observable.

## Processing Flows

### Text Translation

1. WhatsApp sends an inbound text message to the Go webhook.
2. The application verifies the webhook and authorizes the sender against the allowlist.
3. It applies quota, rate-limit, and global safeguard checks before provider use.
4. The application identifies the requested or detected translation direction, calls the translation provider, persists the result, and replies through WhatsApp.

Text translation may remain synchronous only while its provider call fits the webhook response and reliability constraints. This is a recommendation, not a final implementation decision.

### Voice and Audio

1. WhatsApp sends an inbound media message to the Go webhook.
2. The application verifies and authorizes the sender, applies cost safeguards, stores media and durable job metadata, and queues transcription work.
3. A worker sends audio to OpenAI transcription without a language hint and receives the source transcript.
4. The worker sends that transcript in a separate OpenAI text request. Its strict structured response identifies Somali or English, selects the opposite target language, and supplies the translation; other languages fail.
5. The worker makes a third strict structured validation call over the source transcript and proposed translation. A safe corrected translation replaces the proposed text; an invalid or failed validation without a correction fails the job.
6. The worker persists the source transcript, detected and target languages, validated translated text, and state transitions, then sends the text reply through WhatsApp.

### Photo OCR

1. The webhook verifies and authorizes the sender, applies safeguards, stores the image, and creates an OCR job.
2. A worker submits the image to the OCR provider.
3. The workflow translates the extracted Somali or English text into the other language.
4. The result and state transitions are persisted before the WhatsApp reply is sent.

## Asynchronous Work and Reliability

The initial transcription workflow uses `PENDING`, `PROCESSING`, `COMPLETED`, and `FAILED`. PostgreSQL is the durable state source; Redis Streams is a consumer-group queue. Workers atomically claim `PENDING` records, recover abandoned stream messages, and periodically re-enqueue durable pending records.

Jobs must support:

- Idempotent intake for webhook redelivery and repeated messages.
- Safe retries for transient media, provider, queue, and delivery failures.
- Explicit terminal failures that can be inspected without accidental repeated charges.
- Upload/media deduplication where appropriate.
- A record of provider use sufficient to investigate failures and cost safeguards.

Kafka is a future option for asynchronous media-processing workflows when it is justified. It is not required to start the MVP: the queue choice should be made only when the required durability, retry behavior, and operating cost are clear. If Kafka is introduced, event publication must be coordinated with durable state changes to avoid losing or duplicating work.

## Security and Privacy

- Never commit API keys, WhatsApp tokens, database passwords, phone-number allowlists, or other secrets.
- Configuration uses environment variables or secrets management. `.env.example` contains variable names only.
- Runtime secret files, including Kubernetes Secret value manifests, stay outside Git. Versioned Kubernetes manifests may reference a Secret by name but must not embed its values.
- Authorization is evaluated before any paid provider call.
- Persist only the media, transcripts, translations, and corrections needed for the product and future evaluation goals. Retention and deletion policy are not defined yet.

## Deployment Direction

Dockerfiles are versioned in this repository and used to build container images published to Docker Hub. The Ubuntu Raspberry Pi pulls those images, and k3s manages deployments. Docker Compose remains the local-development direction.

The deployment manifests should specify the Docker Hub image and reference externally supplied Kubernetes Secrets. Secret values must not be baked into images, committed in manifests, or supplied as versioned build arguments.

## Recommendations

- Define webhook authentication, WhatsApp signature validation, and outbound-delivery retry behavior before integrating the live WhatsApp account.
- Choose object/file storage and a durable asynchronous-job mechanism before implementing media workflows.
- Establish a small end-to-end test fixture set using non-sensitive audio and images before provider integration.
- Add traces that follow a WhatsApp message through job processing, provider calls, and reply delivery; make provider usage measurable in metrics.

These recommendations are not confirmed product requirements.

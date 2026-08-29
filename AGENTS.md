# Project Context

- Build a Somali-first translation and transcription platform. The first interface is a WhatsApp Business Cloud API bot; a React Native + TypeScript iOS/Android app is later.
- Go is the primary backend language. Start as a modular monolith; add separate workers only for asynchronous or independently scalable work. Python is limited to separate ML-specific workloads, including the local Whisper transcription worker.
- The MVP supports Somali audio to Somali transcription plus English translation, Somali/English text translation, and photo OCR followed by translation. Long-running audio and OCR work must be asynchronous; HTTP webhooks must not wait for it.
- Use provider abstractions for transcription, translation, and OCR. Do not reduce the product to a thin OpenAI wrapper; a custom Somali ASR provider must be possible later.
- Store media outside PostgreSQL. Design job/event state, retries, idempotency, and upload deduplication. Preserve user transcript corrections as possible future evaluation/training data.
- Only allowlisted WhatsApp senders may incur paid processing. Enforce per-user quotas, rate limiting, and global cost safeguards.
- Version Dockerfiles and deployment manifests, publish container images to Docker Hub, and deploy them to the Ubuntu Raspberry Pi with k3s. Kubernetes manifests must reference secrets without containing secret values.
- Never commit secrets, tokens, credentials, or phone-number allowlists. Keep runtime secret files ignored; when configuration is introduced, commit `.env.example` with variable names only.
- Read `docs/architecture.md` before changing system design and `docs/roadmap.md` before expanding scope. Keep MVP, later features, and optional infrastructure distinct.

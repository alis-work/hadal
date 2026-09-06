"""Redis Streams transcription worker. It never logs audio or transcript contents."""
import logging
import json
import mimetypes
import os
import socket
import time
from dataclasses import dataclass
from typing import Protocol
from urllib import error, request


STREAM = "transcription-jobs"
GROUP = "transcription-workers"


@dataclass
class Job:
    id: str
    storage_path: str
    attempts: int = 1


class TransientProviderError(RuntimeError):
    pass


class Repository(Protocol):
    def claim(self, transcription_id: str) -> Job | None: ...
    def complete(self, transcription_id: str, transcript: str, detected_language: str, target_language: str, translated_text: str) -> None: ...
    def fail(self, transcription_id: str, reason: str) -> None: ...
    def retry(self, transcription_id: str, reason: str) -> None: ...
    def pending_ids(self) -> list[str]: ...
    def recover_stalled(self) -> None: ...
    def fail_exhausted(self) -> None: ...


class Transcriber(Protocol):
    def transcribe(self, path: str) -> str: ...


class Translator(Protocol):
    def translate(self, text: str) -> "TranslationResult": ...


class Validator(Protocol):
    def validate(self, transcript: str, translation: "TranslationResult") -> "ValidationResult": ...


@dataclass
class TranslationResult:
    source_language: str
    target_language: str
    translated_text: str


@dataclass
class ValidationResult:
    valid: bool
    corrected_text: str | None = None


def format_result_reply(transcript: str, detected_language: str, target_language: str, translated_text: str) -> str:
    language_names = {"so": "Somali", "en": "English"}
    return (
        f"Source transcript:\n{transcript}\n\n"
        f"Detected language: {language_names[detected_language]} ({detected_language})\n"
        f"Target language: {language_names[target_language]} ({target_language})\n\n"
        f"Translation:\n{translated_text}"
    )


class PostgresRepository:
    def __init__(self, database_url: str, stale_after_seconds: int, max_attempts: int):
        import psycopg

        self.connection = psycopg.connect(database_url)
        self.stale_after_seconds = stale_after_seconds
        self.max_attempts = max_attempts

    def claim(self, transcription_id: str) -> Job | None:
        with self.connection.cursor() as cursor:
            cursor.execute(
                """UPDATE transcriptions SET status = 'PROCESSING', provider_attempt_count = provider_attempt_count + 1,
                          processing_started_at = NOW(), updated_at = NOW()
                   WHERE id = %s AND status = 'PENDING' AND next_attempt_at <= NOW()
                     AND provider_attempt_count < %s
                   RETURNING id::text, storage_path, provider_attempt_count""",
                (transcription_id, self.max_attempts),
            )
            row = cursor.fetchone()
        self.connection.commit()
        return Job(*row) if row else None

    def complete(self, transcription_id: str, transcript: str, detected_language: str, target_language: str, translated_text: str) -> None:
        body = format_result_reply(transcript, detected_language, target_language, translated_text)
        with self.connection.transaction():
            with self.connection.cursor() as cursor:
                cursor.execute(
                    """UPDATE transcriptions SET status = 'COMPLETED', transcript = %s, detected_language = %s,
                       target_language = %s, translated_text = %s,
                       completed_at = NOW(), updated_at = NOW()
                       WHERE id = %s AND status = 'PROCESSING' RETURNING id, sender_id, created_at::date""",
                    (transcript, detected_language, target_language, translated_text, transcription_id),
                )
                completed_row = cursor.fetchone()
                if completed_row is None:
                    raise RuntimeError("transcription was not processing")
                sender_id, usage_date = completed_row[1], completed_row[2]
                cursor.execute(
                    """UPDATE global_quota_usage SET completed_requests = completed_requests + 1, updated_at = NOW()
                       WHERE usage_date = %s AND completed_requests < reserved_requests""",
                    (usage_date,),
                )
                if sender_id is not None:
                    cursor.execute(
                        """UPDATE daily_quota_usage SET completed_requests = completed_requests + 1, updated_at = NOW()
                           WHERE sender_id = %s AND usage_date = %s AND completed_requests < reserved_requests""",
                        (sender_id, usage_date),
                    )
                cursor.execute(
                    """UPDATE whatsapp_inbound_messages
                       SET status = 'COMPLETED', failure_reason = NULL, next_attempt_at = NULL,
                            processing_started_at = NULL, completed_at = NOW(), updated_at = NOW()
                       WHERE transcription_id = %s""",
                    (transcription_id,),
                )
                cursor.execute(
                    """INSERT INTO whatsapp_outbound_messages
                           (source_message_id, recipient_phone, reply_to_whatsapp_message_id, body)
                       SELECT whatsapp_message_id, sender_phone, whatsapp_message_id, %s
                       FROM whatsapp_inbound_messages
                       WHERE transcription_id = %s
                       ON CONFLICT (source_message_id) DO NOTHING""",
                    (body, transcription_id),
                )

    def fail(self, transcription_id: str, reason: str) -> None:
        with self.connection.transaction():
            with self.connection.cursor() as cursor:
                cursor.execute(
                    """UPDATE transcriptions SET status = 'FAILED', failure_reason = %s, updated_at = NOW()
                       WHERE id = %s AND status = 'PROCESSING' RETURNING id""",
                    (reason[:1000], transcription_id),
                )
                if cursor.fetchone() is None:
                    raise RuntimeError("transcription was not processing")
                cursor.execute(
                    """UPDATE whatsapp_inbound_messages
                       SET status = 'FAILED', failure_reason = %s, next_attempt_at = NULL,
                            processing_started_at = NULL, completed_at = NULL, updated_at = NOW()
                       WHERE transcription_id = %s""",
                    (reason[:1000], transcription_id),
                )
                cursor.execute(
                    """INSERT INTO whatsapp_outbound_messages
                           (source_message_id, recipient_phone, reply_to_whatsapp_message_id, body)
                       SELECT whatsapp_message_id, sender_phone, whatsapp_message_id,
                              'Sorry, we could not process your audio. Please try again.'
                       FROM whatsapp_inbound_messages
                       WHERE transcription_id = %s
                       ON CONFLICT (source_message_id) DO NOTHING""",
                    (transcription_id,),
                )

    def retry(self, transcription_id: str, reason: str) -> None:
        with self.connection.cursor() as cursor:
            cursor.execute(
                """UPDATE transcriptions SET status = 'PENDING', failure_reason = NULL,
                          processing_started_at = NULL,
                          next_attempt_at = NOW() + (LEAST(provider_attempt_count, 5) * INTERVAL '10 seconds'),
                          updated_at = NOW()
                   WHERE id = %s AND status = 'PROCESSING'""",
                (transcription_id,),
            )
        self.connection.commit()

    def pending_ids(self) -> list[str]:
        with self.connection.cursor() as cursor:
            cursor.execute(
                """SELECT id::text FROM transcriptions
                   WHERE status = 'PENDING' AND next_attempt_at <= NOW()
                     AND provider_attempt_count < %s ORDER BY created_at""",
                (self.max_attempts,),
            )
            rows = cursor.fetchall()
        self.connection.commit()
        return [row[0] for row in rows]

    def recover_stalled(self) -> None:
        with self.connection.cursor() as cursor:
            cursor.execute(
                """UPDATE transcriptions SET status = 'PENDING', updated_at = NOW()
                   WHERE status = 'PROCESSING'
                   AND updated_at < NOW() - (%s * INTERVAL '1 second')""",
                (self.stale_after_seconds,),
            )
        self.connection.commit()

    def fail_exhausted(self) -> None:
        with self.connection.transaction():
            with self.connection.cursor() as cursor:
                cursor.execute(
                    """UPDATE transcriptions SET status = 'FAILED', failure_reason = 'audio processing retry limit reached',
                              processing_started_at = NULL, updated_at = NOW()
                       WHERE status = 'PENDING' AND provider_attempt_count >= %s
                       RETURNING id""",
                    (self.max_attempts,),
                )
                transcription_ids = [row[0] for row in cursor.fetchall()]
                for transcription_id in transcription_ids:
                    cursor.execute(
                        """UPDATE whatsapp_inbound_messages
                           SET status = 'FAILED', failure_reason = 'audio processing retry limit reached',
                               processing_started_at = NULL, next_attempt_at = NULL, completed_at = NULL, updated_at = NOW()
                           WHERE transcription_id = %s""",
                        (transcription_id,),
                    )
                    cursor.execute(
                        """INSERT INTO whatsapp_outbound_messages
                               (source_message_id, recipient_phone, reply_to_whatsapp_message_id, body)
                           SELECT whatsapp_message_id, sender_phone, whatsapp_message_id,
                                  'Sorry, we could not process your audio. Please try again.'
                           FROM whatsapp_inbound_messages WHERE transcription_id = %s
                           ON CONFLICT (source_message_id) DO NOTHING""",
                        (transcription_id,),
                    )


class OpenAITranscriber:
    def __init__(self, api_key: str, model: str, opener=None):
        self.api_key = api_key
        self.model = model
        self.opener = opener or request.urlopen

    def transcribe(self, path: str) -> str:
        boundary = "----hadal-" + os.urandom(16).hex()
        filename = os.path.basename(path)
        content_type = mimetypes.guess_type(filename)[0] or "application/octet-stream"
        try:
            with open(path, "rb") as audio:
                audio_data = audio.read()
            body = (
                f"--{boundary}\r\n"
                "Content-Disposition: form-data; name=\"model\"\r\n\r\n"
                f"{self.model}\r\n"
                f"--{boundary}\r\n"
                f"Content-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\n"
                f"Content-Type: {content_type}\r\n\r\n"
            ).encode() + audio_data + f"\r\n--{boundary}--\r\n".encode()
            http_request = request.Request(
                "https://api.openai.com/v1/audio/transcriptions",
                data=body,
                headers={"Authorization": f"Bearer {self.api_key}", "Content-Type": f"multipart/form-data; boundary={boundary}"},
                method="POST",
            )
            with self.opener(http_request, timeout=30) as response:
                text = json.loads(response.read())["text"]
            if not isinstance(text, str) or not text.strip():
                raise ValueError
            return text.strip()
        except (OSError, error.URLError, error.HTTPError, KeyError, TypeError, ValueError, json.JSONDecodeError) as failure:
            raise_provider_failure(failure, "OpenAI transcription request failed")


class OpenAITranslator:
    def __init__(self, api_key: str, model: str, opener=None):
        self.api_key = api_key
        self.model = model
        self.opener = opener or request.urlopen

    def translate(self, text: str) -> TranslationResult:
        body = {
            "model": self.model,
            "messages": [
                {"role": "system", "content": "Detect whether the source text is Somali or English and translate it into the other language. Return only JSON matching the requested schema. source_language and target_language must be so or en, and must be opposite languages."},
                {"role": "user", "content": text},
            ],
            "response_format": {
                "type": "json_schema",
                "json_schema": {
                    "name": "translation_result",
                    "strict": True,
                    "schema": {
                        "type": "object",
                        "properties": {
                            "source_language": {"type": "string", "enum": ["so", "en"]},
                            "target_language": {"type": "string", "enum": ["so", "en"]},
                            "translated_text": {"type": "string"},
                        },
                        "required": ["source_language", "target_language", "translated_text"],
                        "additionalProperties": False,
                    },
                },
            },
        }
        try:
            payload = json.dumps(body).encode()
            http_request = request.Request(
                "https://api.openai.com/v1/chat/completions",
                data=payload,
                headers={"Authorization": f"Bearer {self.api_key}", "Content-Type": "application/json"},
                method="POST",
            )
            with self.opener(http_request, timeout=30) as response:
                content = json.loads(response.read())["choices"][0]["message"]["content"]
            result = json.loads(content)
            if set(result) != {"source_language", "target_language", "translated_text"}:
                raise ValueError
            source_language = result["source_language"]
            target_language = result["target_language"]
            translated_text = result["translated_text"]
            if (source_language, target_language) not in {("so", "en"), ("en", "so")} or not isinstance(translated_text, str) or not translated_text.strip():
                raise ValueError
            return TranslationResult(source_language, target_language, translated_text.strip())
        except (error.URLError, error.HTTPError, KeyError, TypeError, ValueError, IndexError, json.JSONDecodeError) as failure:
            raise_provider_failure(failure, "OpenAI translation request failed")


class OpenAIValidator:
    def __init__(self, api_key: str, model: str, opener=None):
        self.api_key = api_key
        self.model = model
        self.opener = opener or request.urlopen

    def validate(self, transcript: str, translation: TranslationResult) -> ValidationResult:
        body = {
            "model": self.model,
            "messages": [
                {"role": "system", "content": "Check whether the proposed translation faithfully translates the source text for the supplied opposite-language pair. Return only JSON matching the requested schema. Set valid to true when it is acceptable. When it is not acceptable, include a corrected_text when a safe correction is available."},
                {"role": "user", "content": json.dumps({"source_transcript": transcript, "source_language": translation.source_language, "target_language": translation.target_language, "proposed_translation": translation.translated_text})},
            ],
            "response_format": {"type": "json_schema", "json_schema": {"name": "translation_validation", "strict": True, "schema": {"type": "object", "properties": {"valid": {"type": "boolean"}, "corrected_text": {"type": ["string", "null"]}}, "required": ["valid", "corrected_text"], "additionalProperties": False}}},
        }
        try:
            http_request = request.Request("https://api.openai.com/v1/chat/completions", data=json.dumps(body).encode(), headers={"Authorization": f"Bearer {self.api_key}", "Content-Type": "application/json"}, method="POST")
            with self.opener(http_request, timeout=30) as response:
                result = json.loads(json.loads(response.read())["choices"][0]["message"]["content"])
            if set(result) != {"valid", "corrected_text"} or not isinstance(result["valid"], bool):
                raise ValueError
            corrected = result["corrected_text"]
            if corrected is not None and (not isinstance(corrected, str) or not corrected.strip()):
                raise ValueError
            return ValidationResult(result["valid"], corrected.strip() if corrected else None)
        except (error.URLError, error.HTTPError, KeyError, TypeError, ValueError, IndexError, json.JSONDecodeError) as failure:
            raise_provider_failure(failure, "OpenAI translation validation request failed")


def raise_provider_failure(failure: Exception, message: str) -> None:
    if isinstance(failure, error.HTTPError):
        if failure.code == 429 or failure.code >= 500:
            raise TransientProviderError(message) from None
        raise RuntimeError(message) from None
    if isinstance(failure, (error.URLError, TimeoutError)):
        raise TransientProviderError(message) from None
    raise RuntimeError(message) from None


class Worker:
    def __init__(self, repository: Repository, client, transcriber: Transcriber, translator: Translator, validator: Validator, consumer: str, reclaim_idle_ms: int = 3_600_000, max_attempts: int = 3):
        self.repository = repository
        self.client = client
        self.transcriber = transcriber
        self.translator = translator
        self.validator = validator
        self.consumer = consumer
        self.reclaim_idle_ms = reclaim_idle_ms
        self.max_attempts = max_attempts

    def process(self, message_id: str, fields: dict) -> None:
        transcription_id = fields.get("transcription_id")
        if not transcription_id:
            logging.error("discarding malformed stream message", extra={"message_id": message_id})
            self.client.xack(STREAM, GROUP, message_id)
            return
        job = self.repository.claim(transcription_id)
        if not job:  # Already completed, failed, or owned by another consumer.
            self.client.xack(STREAM, GROUP, message_id)
            return
        started = time.monotonic()
        logging.info("processing started", extra={"transcription_id": job.id})
        try:
            transcript = self.transcriber.transcribe(job.storage_path)
            if not transcript:
                raise RuntimeError("empty transcript")
            result = self.translator.translate(transcript)
            if (result.source_language, result.target_language) not in {("so", "en"), ("en", "so")}:
                raise ValueError("unsupported detected language")
            if not result.translated_text:
                raise RuntimeError("empty translation")
            validation = self.validator.validate(transcript, result)
            if not validation.valid and not validation.corrected_text:
                raise RuntimeError("translation validation failed")
            translated_text = validation.corrected_text or result.translated_text
            self.repository.complete(job.id, transcript, result.source_language, result.target_language, translated_text)
            try:
                os.remove(job.storage_path)
            except FileNotFoundError:
                pass
            except OSError:
                logging.error("could not delete completed audio", extra={"transcription_id": job.id})
            logging.info("transcription completed", extra={"transcription_id": job.id, "duration_ms": round((time.monotonic() - started) * 1000)})
        except Exception as error:
            if isinstance(error, TransientProviderError) and job.attempts < self.max_attempts:
                try:
                    self.repository.retry(job.id, "provider temporarily unavailable")
                    logging.warning("audio processing scheduled for retry", extra={"transcription_id": job.id, "attempt": job.attempts})
                    self.client.xack(STREAM, GROUP, message_id)
                except Exception:
                    logging.error("could not schedule audio processing retry", extra={"transcription_id": job.id})
                return
            reason = "unsupported detected language" if isinstance(error, ValueError) and str(error) == "unsupported detected language" else "audio processing failed"
            try:
                self.repository.fail(job.id, reason)
                logging.error("audio processing failed", extra={"transcription_id": job.id, "duration_ms": round((time.monotonic() - started) * 1000)})
            except Exception:
                # Leave this Streams message pending when durable failure state cannot be saved.
                logging.error("could not persist audio processing failure", extra={"transcription_id": job.id})
                return
        self.client.xack(STREAM, GROUP, message_id)

    def recover_pending(self) -> None:
        # This also recovers records created when Redis was briefly unavailable.
        self.repository.recover_stalled()
        self.repository.fail_exhausted()
        for transcription_id in self.repository.pending_ids():
            self.client.xadd(STREAM, {"transcription_id": transcription_id})

    def run(self) -> None:
        try:
            self.client.xgroup_create(STREAM, GROUP, id="0", mkstream=True)
        except Exception as error:
            if "BUSYGROUP" not in str(error):
                raise
        last_recovery = 0.0
        while True:
            if time.monotonic() - last_recovery > 60:
                self.recover_pending()
                last_recovery = time.monotonic()
            _, claimed, _ = self.client.xautoclaim(STREAM, GROUP, self.consumer, min_idle_time=self.reclaim_idle_ms, start_id="0-0", count=10)
            for message_id, fields in claimed:
                self.process(message_id, fields)
            messages = self.client.xreadgroup(GROUP, self.consumer, {STREAM: ">"}, count=1, block=5_000)
            for _, entries in messages:
                for message_id, fields in entries:
                    self.process(message_id, fields)


def main() -> None:
    import redis

    logging.basicConfig(level=os.getenv("LOG_LEVEL", "INFO"), format="%(asctime)s %(levelname)s %(message)s")
    database_url = os.environ["DATABASE_URL"]
    redis_url = os.environ["REDIS_URL"]
    consumer = os.getenv("WORKER_CONSUMER", socket.gethostname())
    stale_after_seconds = int(os.getenv("PROCESSING_STALE_AFTER_SECONDS", "3600"))
    max_attempts = int(os.getenv("PROVIDER_MAX_ATTEMPTS", "3"))
    api_key = os.getenv("OPENAI_API_KEY")
    if not api_key:
        raise RuntimeError("OPENAI_API_KEY must be set for the worker to start")
    transcriber = OpenAITranscriber(api_key, os.getenv("OPENAI_TRANSCRIPTION_MODEL", "gpt-transcribe"))
    translator = OpenAITranslator(api_key, os.getenv("OPENAI_TRANSLATION_MODEL", "gpt-4o-mini"))
    validator = OpenAIValidator(api_key, os.getenv("OPENAI_TRANSLATION_MODEL", "gpt-4o-mini"))
    Worker(PostgresRepository(database_url, stale_after_seconds, max_attempts), redis.Redis.from_url(redis_url, decode_responses=True), transcriber, translator, validator, consumer, stale_after_seconds * 1000, max_attempts).run()


if __name__ == "__main__":
    main()

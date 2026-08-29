"""Redis Streams transcription worker. It never logs audio or transcript contents."""
import logging
import os
import socket
import time
from dataclasses import dataclass
from typing import Protocol


STREAM = "transcription-jobs"
GROUP = "transcription-workers"


@dataclass
class Job:
    id: str
    storage_path: str


class Repository(Protocol):
    def claim(self, transcription_id: str) -> Job | None: ...
    def complete(self, transcription_id: str, transcript: str) -> None: ...
    def fail(self, transcription_id: str, reason: str) -> None: ...
    def pending_ids(self) -> list[str]: ...
    def recover_stalled(self) -> None: ...


class Transcriber(Protocol):
    def transcribe(self, path: str) -> str: ...


class PostgresRepository:
    def __init__(self, database_url: str, stale_after_seconds: int):
        import psycopg

        self.connection = psycopg.connect(database_url)
        self.stale_after_seconds = stale_after_seconds

    def claim(self, transcription_id: str) -> Job | None:
        with self.connection.cursor() as cursor:
            cursor.execute(
                """UPDATE transcriptions SET status = 'PROCESSING', processing_started_at = NOW(), updated_at = NOW()
                   WHERE id = %s AND status = 'PENDING' RETURNING id::text, storage_path""",
                (transcription_id,),
            )
            row = cursor.fetchone()
        self.connection.commit()
        return Job(*row) if row else None

    def complete(self, transcription_id: str, transcript: str) -> None:
        with self.connection.cursor() as cursor:
            cursor.execute(
                """UPDATE transcriptions SET status = 'COMPLETED', transcript = %s,
                   completed_at = NOW(), updated_at = NOW()
                   WHERE id = %s AND status = 'PROCESSING'""",
                (transcript, transcription_id),
            )
        self.connection.commit()

    def fail(self, transcription_id: str, reason: str) -> None:
        with self.connection.cursor() as cursor:
            cursor.execute(
                """UPDATE transcriptions SET status = 'FAILED', failure_reason = %s, updated_at = NOW()
                   WHERE id = %s AND status = 'PROCESSING'""",
                (reason[:1000], transcription_id),
            )
        self.connection.commit()

    def pending_ids(self) -> list[str]:
        with self.connection.cursor() as cursor:
            cursor.execute("SELECT id::text FROM transcriptions WHERE status = 'PENDING' ORDER BY created_at")
            rows = cursor.fetchall()
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


class FasterWhisperTranscriber:
    def __init__(self, model_name: str, compute_type: str):
        from faster_whisper import WhisperModel

        self.model = WhisperModel(model_name, compute_type=compute_type)

    def transcribe(self, path: str) -> str:
        segments, _ = self.model.transcribe(path, language="so", task="transcribe")
        return " ".join(segment.text.strip() for segment in segments).strip()


class Worker:
    def __init__(self, repository: Repository, client, transcriber: Transcriber, consumer: str, reclaim_idle_ms: int = 3_600_000):
        self.repository = repository
        self.client = client
        self.transcriber = transcriber
        self.consumer = consumer
        self.reclaim_idle_ms = reclaim_idle_ms

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
                raise RuntimeError("transcriber returned an empty transcript")
            self.repository.complete(job.id, transcript)
            try:
                os.remove(job.storage_path)
            except FileNotFoundError:
                pass
            except OSError:
                logging.exception("could not delete completed audio", extra={"transcription_id": job.id})
            logging.info("transcription completed", extra={"transcription_id": job.id, "duration_ms": round((time.monotonic() - started) * 1000)})
        except Exception as error:
            try:
                self.repository.fail(job.id, str(error))
                logging.exception("transcription failed", extra={"transcription_id": job.id, "duration_ms": round((time.monotonic() - started) * 1000)})
            except Exception:
                # Leave this Streams message pending when durable failure state cannot be saved.
                logging.exception("could not persist transcription failure", extra={"transcription_id": job.id})
                return
        self.client.xack(STREAM, GROUP, message_id)

    def recover_pending(self) -> None:
        # This also recovers records created when Redis was briefly unavailable.
        self.repository.recover_stalled()
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
    model = os.getenv("WHISPER_MODEL", "small")
    compute_type = os.getenv("WHISPER_COMPUTE_TYPE", "int8")
    stale_after_seconds = int(os.getenv("PROCESSING_STALE_AFTER_SECONDS", "3600"))
    Worker(PostgresRepository(database_url, stale_after_seconds), redis.Redis.from_url(redis_url, decode_responses=True), FasterWhisperTranscriber(model, compute_type), consumer, stale_after_seconds * 1000).run()


if __name__ == "__main__":
    main()

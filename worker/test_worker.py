import json
import os
import tempfile
import unittest

from worker.worker import GROUP, STREAM, Job, OpenAITranscriber, OpenAITranslator, OpenAIValidator, TransientProviderError, TranslationResult, ValidationResult, Worker, format_result_reply


class Repository:
    def __init__(self, job, status="PENDING"):
        self.job = job
        self.status = status
        self.completed = []
        self.failed = []
        self.retried = []

    def claim(self, _):
        if self.status != "PENDING":
            return None
        self.status = "PROCESSING"
        return self.job

    def complete(self, job_id, transcript, detected_language, target_language, translated_text):
        self.completed.append((job_id, transcript, detected_language, target_language, translated_text))
        self.status = "COMPLETED"

    def fail(self, job_id, reason):
        self.failed.append((job_id, reason))
        self.status = "FAILED"

    def retry(self, job_id, reason):
        self.retried.append((job_id, reason))
        self.status = "PENDING"

    def pending_ids(self):
        return []

    def recover_stalled(self):
        pass

    def fail_exhausted(self):
        pass


class Client:
    def __init__(self):
        self.acks = []

    def xack(self, stream, group, message):
        self.acks.append((stream, group, message))


class Transcriber:
    def __init__(self, transcript=None, error=None):
        self.transcript = transcript
        self.error = error

    def transcribe(self, _):
        if self.error:
            raise self.error
        return self.transcript


class Translator:
    def __init__(self, result=None, error=None):
        self.result = result
        self.error = error
        self.calls = []

    def translate(self, text):
        self.calls.append(text)
        if self.error:
            raise self.error
        return self.result


class Validator:
    def __init__(self, result=None, error=None):
        self.result = result
        self.error = error

    def validate(self, transcript, translation):
        if self.error:
            raise self.error
        return self.result


class WorkerTests(unittest.TestCase):
    def test_result_reply_formats_bilingual_result(self):
        self.assertEqual(
            format_result_reply("Salaan", "so", "en", "Hello"),
            "Source transcript:\nSalaan\n\nDetected language: Somali (so)\n"
            "Target language: English (en)\n\nTranslation:\nHello",
        )

    def test_somali_classification_completes_with_english_translation(self):
        repo, client = Repository(Job("job-1", "/audio")), Client()
        translator = Translator(TranslationResult("so", "en", "Hello"))
        Worker(repo, client, Transcriber("Salaan"), translator, Validator(ValidationResult(True)), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.completed, [("job-1", "Salaan", "so", "en", "Hello")])
        self.assertEqual(translator.calls, ["Salaan"])
        self.assertEqual(repo.status, "COMPLETED")
        self.assertEqual(client.acks, [(STREAM, GROUP, "1-0")])

    def test_english_classification_completes_with_somali_translation(self):
        repo, client = Repository(Job("job-1", "/audio")), Client()
        translator = Translator(TranslationResult("en", "so", "Salaan"))
        Worker(repo, client, Transcriber("Hello"), translator, Validator(ValidationResult(True)), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.completed, [("job-1", "Hello", "en", "so", "Salaan")])
        self.assertEqual(translator.calls, ["Hello"])

    def test_failure_is_persisted_without_provider_error(self):
        repo, client = Repository(Job("job-1", "/audio")), Client()
        Worker(repo, client, Transcriber(error=RuntimeError("sensitive provider detail")), Translator(), Validator(), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.failed, [("job-1", "audio processing failed")])
        self.assertEqual(repo.status, "FAILED")
        self.assertEqual(client.acks, [(STREAM, GROUP, "1-0")])

    def test_transient_provider_failure_is_retried(self):
        repo, client = Repository(Job("job-1", "/audio", attempts=1)), Client()
        Worker(repo, client, Transcriber(error=TransientProviderError("temporary")), Translator(), Validator(), "test", max_attempts=3).process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.retried, [("job-1", "provider temporarily unavailable")])
        self.assertEqual(repo.failed, [])
        self.assertEqual(client.acks, [(STREAM, GROUP, "1-0")])

    def test_unsupported_translation_classification_fails(self):
        repo, client = Repository(Job("job-1", "/audio")), Client()
        Worker(repo, client, Transcriber("Bonjour"), Translator(TranslationResult("fr", "en", "Hello")), Validator(ValidationResult(True)), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.failed, [("job-1", "unsupported detected language")])
        self.assertEqual(client.acks, [(STREAM, GROUP, "1-0")])

    def test_completed_duplicate_is_safe_noop(self):
        repo, client = Repository(None, status="COMPLETED"), Client()
        Worker(repo, client, Transcriber("ignored"), Translator(TranslationResult("so", "en", "ignored")), Validator(ValidationResult(True)), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.completed, [])
        self.assertEqual(repo.status, "COMPLETED")
        self.assertEqual(client.acks, [(STREAM, GROUP, "1-0")])

    def test_validator_correction_is_persisted(self):
        repo, client = Repository(Job("job-1", "/audio")), Client()
        Worker(repo, client, Transcriber("Salaan"), Translator(TranslationResult("so", "en", "Hi")), Validator(ValidationResult(False, "Hello")), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.completed[0][-1], "Hello")

    def test_validation_failure_fails_job_safely(self):
        repo, client = Repository(Job("job-1", "/audio")), Client()
        Worker(repo, client, Transcriber("Salaan"), Translator(TranslationResult("so", "en", "Hello")), Validator(ValidationResult(False)), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.failed, [("job-1", "audio processing failed")])


class OpenAIClientTests(unittest.TestCase):
    class Response:
        def __init__(self, body):
            self.body = body

        def read(self):
            return self.body

        def __enter__(self):
            return self

        def __exit__(self, *_):
            return False

    def test_transcriber_posts_multipart_without_language(self):
        requests = []

        def opener(http_request, timeout):
            requests.append((http_request, timeout))
            return self.Response(b'{"text":"Salaan"}')

        with tempfile.NamedTemporaryFile(suffix=".m4a") as audio:
            audio.write(b"audio-bytes")
            audio.flush()
            self.assertEqual(OpenAITranscriber("test-key", "test-model", opener).transcribe(audio.name), "Salaan")
            filename = os.path.basename(audio.name).encode()
        http_request, timeout = requests[0]
        self.assertEqual(timeout, 30)
        self.assertEqual(http_request.full_url, "https://api.openai.com/v1/audio/transcriptions")
        self.assertEqual(http_request.get_header("Authorization"), "Bearer test-key")
        self.assertIn(b'name="model"\r\n\r\ntest-model', http_request.data)
        self.assertIn(b'filename="' + filename + b'"', http_request.data)
        self.assertNotIn(b'name="language"', http_request.data)

    def test_translator_posts_structured_request_and_parses_result(self):
        requests = []

        def opener(http_request, timeout):
            requests.append((http_request, timeout))
            return self.Response(b'{"choices":[{"message":{"content":"{\\"source_language\\":\\"so\\",\\"target_language\\":\\"en\\",\\"translated_text\\":\\"Hello\\"}"}}]}')

        result = OpenAITranslator("test-key", "test-model", opener).translate("Salaan")
        self.assertEqual(result, TranslationResult("so", "en", "Hello"))
        http_request, timeout = requests[0]
        self.assertEqual(timeout, 30)
        self.assertEqual(http_request.full_url, "https://api.openai.com/v1/chat/completions")
        self.assertEqual(http_request.get_header("Authorization"), "Bearer test-key")
        body = json.loads(http_request.data)
        self.assertEqual(body["model"], "test-model")
        self.assertTrue(body["response_format"]["json_schema"]["strict"])

    def test_translator_rejects_non_opposite_language_pair(self):
        def opener(_, timeout):
            return self.Response(b'{"choices":[{"message":{"content":"{\\"source_language\\":\\"so\\",\\"target_language\\":\\"so\\",\\"translated_text\\":\\"Salaan\\"}"}}]}')

        with self.assertRaisesRegex(RuntimeError, "OpenAI translation request failed"):
            OpenAITranslator("test-key", "test-model", opener).translate("Salaan")

    def test_validator_posts_structured_request_and_parses_correction(self):
        requests = []
        def opener(http_request, timeout):
            requests.append((http_request, timeout))
            return self.Response(b'{"choices":[{"message":{"content":"{\\"valid\\":false,\\"corrected_text\\":\\"Hello\\"}"}}]}')
        result = OpenAIValidator("test-key", "test-model", opener).validate("Salaan", TranslationResult("so", "en", "Hi"))
        self.assertEqual(result, ValidationResult(False, "Hello"))
        body = json.loads(requests[0][0].data)
        self.assertTrue(body["response_format"]["json_schema"]["strict"])

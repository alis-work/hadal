import unittest

from worker.worker import GROUP, STREAM, Job, Worker


class Repository:
    def __init__(self, job, status="PENDING"): self.job = job; self.status = status; self.completed = []; self.failed = []
    def claim(self, _):
        if self.status != "PENDING": return None
        self.status = "PROCESSING"
        return self.job
    def complete(self, job_id, transcript): self.completed.append((job_id, transcript)); self.status = "COMPLETED"
    def fail(self, job_id, reason): self.failed.append((job_id, reason)); self.status = "FAILED"
    def pending_ids(self): return []
    def recover_stalled(self): pass


class Client:
    def __init__(self): self.acks = []
    def xack(self, stream, group, message): self.acks.append((stream, group, message))


class Transcriber:
    def __init__(self, result=None, error=None): self.result = result; self.error = error
    def transcribe(self, _):
        if self.error: raise self.error
        return self.result


class WorkerTests(unittest.TestCase):
    def test_success_completes_and_acknowledges(self):
        repo, client = Repository(Job("job-1", "/audio")), Client()
        Worker(repo, client, Transcriber("qoraal"), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.completed, [("job-1", "qoraal")])
        self.assertEqual(repo.status, "COMPLETED")
        self.assertEqual(client.acks, [(STREAM, GROUP, "1-0")])

    def test_failure_is_persisted_and_acknowledged(self):
        repo, client = Repository(Job("job-1", "/audio")), Client()
        Worker(repo, client, Transcriber(error=RuntimeError("model failed")), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.failed, [("job-1", "model failed")])
        self.assertEqual(repo.status, "FAILED")
        self.assertEqual(client.acks, [(STREAM, GROUP, "1-0")])

    def test_completed_duplicate_is_safe_noop(self):
        repo, client = Repository(None, status="COMPLETED"), Client()
        Worker(repo, client, Transcriber("ignored"), "test").process("1-0", {"transcription_id": "job-1"})
        self.assertEqual(repo.completed, [])
        self.assertEqual(repo.status, "COMPLETED")
        self.assertEqual(client.acks, [(STREAM, GROUP, "1-0")])

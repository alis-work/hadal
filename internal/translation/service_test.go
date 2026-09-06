package translation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRepository struct {
	submission    Submission
	submitRecord  Record
	inserted      bool
	submitErr     error
	claimed       Record
	claimErr      error
	completed     *Result
	retried       bool
	retryDelay    time.Duration
	failed        bool
	failureReason string
	exhausted     bool
	exhaustedErr  error
}

func (repository *fakeRepository) Submit(_ context.Context, submission Submission) (Record, bool, error) {
	repository.submission = submission
	return repository.submitRecord, repository.inserted, repository.submitErr
}
func (repository *fakeRepository) Claim(context.Context, int, time.Duration) (Record, error) {
	return repository.claimed, repository.claimErr
}
func (repository *fakeRepository) Complete(_ context.Context, _ uuid.UUID, result Result) error {
	repository.completed = &result
	return nil
}
func (repository *fakeRepository) Retry(_ context.Context, _ uuid.UUID, _ string, delay time.Duration) error {
	repository.retried = true
	repository.retryDelay = delay
	return nil
}
func (repository *fakeRepository) Fail(_ context.Context, _ uuid.UUID, reason string) error {
	repository.failed = true
	repository.failureReason = reason
	return nil
}
func (repository *fakeRepository) FailExhausted(context.Context, int, time.Duration) (bool, error) {
	return repository.exhausted, repository.exhaustedErr
}

type fakeProvider struct {
	result Result
	err    error
	calls  int
}

func (provider *fakeProvider) Translate(context.Context, string) (Result, error) {
	provider.calls++
	return provider.result, provider.err
}

func TestServiceSubmitPersistsOnlyAndIsIdempotencyReady(t *testing.T) {
	id := uuid.New()
	repository := &fakeRepository{submitRecord: Record{ID: id, Status: Pending}, inserted: true}
	service := NewService(repository)
	record, err := service.Submit(context.Background(), 42, "+252611111111", "Salaan")
	if err != nil || record.ID != id {
		t.Fatalf("unexpected submission: %+v, %v", record, err)
	}
	if repository.submission.ID == uuid.Nil || repository.submission.InboundMessageID != 42 || repository.submission.SenderPhone != "+252611111111" || repository.submission.SourceText != "Salaan" {
		t.Fatalf("unexpected repository submission: %+v", repository.submission)
	}
}

func TestServiceRejectsInvalidTextWithoutRepositoryWork(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	if _, err := service.Submit(context.Background(), 1, "+252611111111", " "); !errors.Is(err, ErrEmptySource) {
		t.Fatalf("expected empty source, got %v", err)
	}
	if _, err := service.Submit(context.Background(), 1, "+252611111111", strings.Repeat("a", MaxSourceBytes+1)); !errors.Is(err, ErrSourceTooLong) {
		t.Fatalf("expected source limit, got %v", err)
	}
	if repository.submission.ID != uuid.Nil {
		t.Fatal("invalid source reached repository")
	}
}

func TestServicePreservesPolicyFailures(t *testing.T) {
	for _, policyError := range []error{ErrSenderNotFound, ErrSenderDisabled, ErrSenderAdmin, ErrDailyQuota, ErrRecruiterQuota, ErrGlobalQuota, ErrInvalidInbound} {
		repository := &fakeRepository{submitErr: policyError}
		_, err := NewService(repository).Submit(context.Background(), 1, "+252611111111", "hello")
		if !errors.Is(err, policyError) {
			t.Errorf("expected %v, got %v", policyError, err)
		}
	}
}

func TestRunnerCompletesSuccessfulTranslation(t *testing.T) {
	id := uuid.New()
	repository := &fakeRepository{claimed: Record{ID: id, SourceText: "Hello", Attempts: 1}}
	provider := &fakeProvider{result: Result{SourceLanguage: English, TargetLanguage: Somali, TranslatedText: "Salaan"}}
	worked, err := NewRunner(repository, provider, 3, time.Minute).ProcessOne(context.Background())
	if err != nil || !worked || repository.completed == nil || repository.completed.TranslatedText != "Salaan" || repository.retried || repository.failed {
		t.Fatalf("unexpected run: worked=%v err=%v repo=%+v", worked, err, repository)
	}
}

func TestRunnerRetriesTransientFailureWithBackoff(t *testing.T) {
	repository := &fakeRepository{claimed: Record{ID: uuid.New(), SourceText: "Hello", Attempts: 2}}
	provider := &fakeProvider{err: transient(errors.New("rate limited"))}
	worked, err := NewRunner(repository, provider, 3, time.Minute).ProcessOne(context.Background())
	if err != nil || !worked || !repository.retried || repository.retryDelay != 20*time.Second || repository.failed {
		t.Fatalf("unexpected retry: worked=%v err=%v repo=%+v", worked, err, repository)
	}
}

func TestRunnerFailsPermanentAndExhaustedProviderFailures(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		err      error
	}{
		{"permanent", 1, errors.New("invalid output")},
		{"transient exhausted", 3, transient(errors.New("unavailable"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{claimed: Record{ID: uuid.New(), SourceText: "Hello", Attempts: test.attempts}}
			worked, err := NewRunner(repository, &fakeProvider{err: test.err}, 3, time.Minute).ProcessOne(context.Background())
			if err != nil || !worked || !repository.failed || repository.retried || repository.failureReason == "" {
				t.Fatalf("unexpected failure handling: worked=%v err=%v repo=%+v", worked, err, repository)
			}
		})
	}
}

func TestRunnerFailsExhaustedDurableJobBeforeClaiming(t *testing.T) {
	repository := &fakeRepository{exhausted: true, claimErr: errors.New("must not claim")}
	provider := &fakeProvider{}
	worked, err := NewRunner(repository, provider, 3, time.Minute).ProcessOne(context.Background())
	if err != nil || !worked || provider.calls != 0 {
		t.Fatalf("unexpected exhaustion handling: worked=%v calls=%d err=%v", worked, provider.calls, err)
	}
}

func TestRunnerReturnsIdleWithoutProviderCall(t *testing.T) {
	repository := &fakeRepository{claimErr: ErrNoWork}
	provider := &fakeProvider{}
	worked, err := NewRunner(repository, provider, 3, time.Minute).ProcessOne(context.Background())
	if err != nil || worked || provider.calls != 0 {
		t.Fatalf("unexpected idle result: worked=%v calls=%d err=%v", worked, provider.calls, err)
	}
}

func TestResultValidation(t *testing.T) {
	question := "What did you mean?"
	empty := " "
	valid := []Result{
		{SourceLanguage: Somali, TargetLanguage: English, TranslatedText: "Hello"},
		{SourceLanguage: English, TargetLanguage: Somali, ClarificationRequired: true, ClarificationQuestion: &question},
	}
	for _, result := range valid {
		if err := result.Validate(); err != nil {
			t.Errorf("valid result rejected: %+v: %v", result, err)
		}
	}
	invalid := []Result{
		{SourceLanguage: Somali, TargetLanguage: Somali, TranslatedText: "x"},
		{SourceLanguage: Somali, TargetLanguage: English},
		{SourceLanguage: Somali, TargetLanguage: English, TranslatedText: "x", ClarificationQuestion: &question},
		{SourceLanguage: Somali, TargetLanguage: English, ClarificationRequired: true},
		{SourceLanguage: Somali, TargetLanguage: English, TranslatedText: "x", InterpretedSource: &empty},
	}
	for _, result := range invalid {
		if !errors.Is(result.Validate(), ErrInvalidResult) {
			t.Errorf("invalid result accepted: %+v", result)
		}
	}
}

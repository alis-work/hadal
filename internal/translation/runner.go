package translation

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const safeFailureReply = "Sorry, we could not translate that message. Please try again."

type Runner struct {
	repository  Repository
	provider    Provider
	maxAttempts int
	staleAfter  time.Duration
}

func NewRunner(repository Repository, provider Provider, maxAttempts int, staleAfter time.Duration) *Runner {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if staleAfter <= 0 {
		staleAfter = 5 * time.Minute
	}
	return &Runner{repository: repository, provider: provider, maxAttempts: maxAttempts, staleAfter: staleAfter}
}

func (r *Runner) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			worked, err := r.ProcessOne(ctx)
			if err != nil {
				return err
			}
			if worked {
				timer.Reset(0)
			} else {
				timer.Reset(interval)
			}
		}
	}
}

func (r *Runner) ProcessOne(ctx context.Context) (bool, error) {
	failed, err := r.repository.FailExhausted(ctx, r.maxAttempts, r.staleAfter)
	if err != nil {
		return false, fmt.Errorf("fail exhausted translation: %w", err)
	}
	if failed {
		return true, nil
	}
	record, err := r.repository.Claim(ctx, r.maxAttempts, r.staleAfter)
	if errors.Is(err, ErrNoWork) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim translation: %w", err)
	}
	result, providerErr := r.provider.Translate(ctx, record.SourceText)
	if providerErr == nil {
		providerErr = result.Validate()
	}
	if providerErr == nil {
		if err := r.repository.Complete(ctx, record.ID, result); err != nil {
			return true, fmt.Errorf("complete translation: %w", err)
		}
		return true, nil
	}
	if IsTransient(providerErr) && record.Attempts < r.maxAttempts {
		if err := r.repository.Retry(ctx, record.ID, providerErr.Error(), retryBackoff(record.Attempts)); err != nil {
			return true, fmt.Errorf("retry translation: %w", err)
		}
		return true, nil
	}
	if err := r.repository.Fail(ctx, record.ID, providerErr.Error()); err != nil {
		return true, fmt.Errorf("fail translation: %w", err)
	}
	return true, nil
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		attempt = 5
	}
	return time.Duration(attempt) * 10 * time.Second
}

func FormatReply(result Result) string {
	if result.ClarificationRequired {
		return *result.ClarificationQuestion
	}
	return result.TranslatedText
}

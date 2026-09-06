package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Dispatcher struct {
	repository    Repository
	client        Client
	phoneNumberID string
	maxAttempts   int
}

func NewDispatcher(repository Repository, client Client, phoneNumberID string, maxAttempts int) *Dispatcher {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	return &Dispatcher{repository: repository, client: client, phoneNumberID: phoneNumberID, maxAttempts: maxAttempts}
}

func (d *Dispatcher) DispatchOne(ctx context.Context) (bool, error) {
	message, err := d.repository.ClaimOutbound(ctx, d.maxAttempts)
	if errors.Is(err, ErrNoWork) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim outbound whatsapp message: %w", err)
	}
	providerID, err := d.client.SendText(ctx, d.phoneNumberID, message.Sender, message.ReplyTo, message.Text)
	if err != nil {
		if IsAmbiguous(err) {
			if unknownErr := d.repository.MarkOutboundUnknown(ctx, message.ID, err.Error()); unknownErr != nil {
				return true, fmt.Errorf("mark outbound whatsapp delivery unknown: %w", unknownErr)
			}
			return true, nil
		}
		if IsTransient(err) && message.Attempts < d.maxAttempts {
			if retryErr := d.repository.RetryOutbound(ctx, message.ID, err.Error()); retryErr != nil {
				return true, fmt.Errorf("retry outbound whatsapp message: %w", retryErr)
			}
			return true, nil
		}
		if failErr := d.repository.FailOutbound(ctx, message.ID, err.Error()); failErr != nil {
			return true, fmt.Errorf("fail outbound whatsapp message: %w", failErr)
		}
		return true, nil
	}
	if err := d.repository.MarkOutboundSent(ctx, message.ID, providerID); err != nil {
		return true, fmt.Errorf("mark outbound whatsapp message sent: %w", err)
	}
	return true, nil
}

func (d *Dispatcher) Runner(interval time.Duration) Runner {
	return Runner{Process: d.DispatchOne, Interval: interval}
}

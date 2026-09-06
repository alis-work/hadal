package whatsapp

import (
	"context"
	"errors"
	"time"
)

var ErrNoWork = errors.New("no whatsapp work available")

type InboundMessage struct {
	ID                int64
	ProviderMessageID string
	Sender            string
	Type              string
	Text              string
	MediaID           string
	MIMEType          string
	Attempts          int
}

type OutboundMessage struct {
	ID       int64
	Sender   string
	ReplyTo  string
	Text     string
	Attempts int
}

// InboundStore makes webhook redelivery idempotent by provider message ID.
type InboundStore interface {
	SaveInbound(context.Context, InboundMessage) (inserted bool, err error)
}

// Repository implementations must atomically mark an inbound message complete
// and insert its optional reply in CompleteInbound.
type Repository interface {
	InboundStore
	ReconcileInbound(context.Context, int) (bool, error)
	ClaimInbound(context.Context, int) (InboundMessage, error)
	CompleteInbound(context.Context, int64, *OutboundMessage) error
	RetryInbound(context.Context, int64, string) error
	ClaimOutbound(context.Context, int) (OutboundMessage, error)
	MarkOutboundSent(context.Context, int64, string) error
	RetryOutbound(context.Context, int64, string) error
	FailOutbound(context.Context, int64, string) error
	MarkOutboundUnknown(context.Context, int64, string) error
}

type Runner struct {
	Process  func(context.Context) (bool, error)
	Interval time.Duration
}

func (r Runner) Run(ctx context.Context) error {
	interval := r.Interval
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
			worked, err := r.Process(ctx)
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

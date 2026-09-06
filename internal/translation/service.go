package translation

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

// Submit only reserves policy and persists work. Provider execution belongs to Runner.
func (s *Service) Submit(ctx context.Context, inboundMessageID int64, senderPhone, sourceText string) (Record, error) {
	if err := validateSource(sourceText); err != nil {
		return Record{}, err
	}
	record, _, err := s.repository.Submit(ctx, Submission{ID: uuid.New(), InboundMessageID: inboundMessageID, SenderPhone: senderPhone, SourceText: sourceText})
	if err != nil {
		return Record{}, fmt.Errorf("submit translation: %w", err)
	}
	return record, nil
}

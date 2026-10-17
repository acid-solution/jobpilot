package jdimport

import (
	"context"
	"errors"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

type unreadTarget struct{}

func (unreadTarget) Current(context.Context, uuid.UUID) (target.Target, error) {
	panic("garbage must be rejected before reading business data")
}

func TestImportRejectsGarbageBeforeCreatingAnyTask(t *testing.T) {
	s := Service{Targets: unreadTarget{}}
	for _, text := range []string{"", "https://example.com/jobs/123", "###########", "1234567890"} {
		if _, err := s.Start(t.Context(), uuid.New(), text, nil); !errors.Is(err, market.ErrInvalidJDText) {
			t.Fatalf("expected invalid_jd_text, got %v", err)
		}
	}
}

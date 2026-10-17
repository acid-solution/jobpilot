package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agentcontext"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/google/uuid"
)

func (s *Service) MemorySnapshot(ctx context.Context, user uuid.UUID) (assistantmemory.Snapshot, error) {
	goal, e := s.memoryGoal(ctx, user)
	if e != nil {
		return assistantmemory.Snapshot{}, e
	}
	return s.Memories.Snapshot(ctx, user, goal)
}
func (s *Service) MemoryProposals(ctx context.Context, user uuid.UUID) ([]assistantmemory.Proposal, error) {
	goal, e := s.memoryGoal(ctx, user)
	if e != nil {
		return nil, e
	}
	return s.Memories.Proposals(ctx, user, goal)
}
func (s *Service) memoryTransaction(ctx context.Context, user uuid.UUID, fn func(context.Context, string) error) error {
	if s.Transactions == nil {
		return errors.New("memory transaction unavailable")
	}
	return s.Transactions.WithinUserTransaction(ctx, user, func(tx context.Context) error {
		goal, e := s.memoryGoal(tx, user)
		if e != nil {
			return e
		}
		return fn(tx, goal)
	})
}
func (s *Service) SaveMemory(ctx context.Context, user uuid.UUID, id *uuid.UUID, w assistantmemory.Write) (m assistantmemory.Memory, err error) {
	if !s.contextOptions().MemoryEnabled {
		return m, assistantmemory.ErrInvalid
	}
	err = s.memoryTransaction(ctx, user, func(tx context.Context, goal string) error {
		var e error
		m, e = s.Memories.Save(tx, user, goal, id, w)
		return e
	})
	return
}
func (s *Service) ForgetMemory(ctx context.Context, user, id uuid.UUID, w assistantmemory.Write) error {
	return s.memoryTransaction(ctx, user, func(tx context.Context, goal string) error {
		return s.Memories.Forget(tx, user, goal, id, w.Revision, w.Hash)
	})
}
func (s *Service) ResolveMemoryEdited(ctx context.Context, user, id uuid.UUID, approve bool, digest string, edited json.RawMessage) (p assistantmemory.Proposal, err error) {
	if !s.contextOptions().MemoryEnabled {
		return p, assistantmemory.ErrInvalid
	}
	err = s.memoryTransaction(ctx, user, func(tx context.Context, goal string) error {
		var e error
		if len(edited) > 0 {
			r, ok := s.Memories.(interface {
				ResolveEdited(context.Context, uuid.UUID, string, uuid.UUID, bool, string, json.RawMessage) (assistantmemory.Proposal, error)
			})
			if !ok {
				return assistantmemory.ErrInvalid
			}
			p, e = r.ResolveEdited(tx, user, goal, id, approve, digest, edited)
		} else {
			p, e = s.Memories.Resolve(tx, user, goal, id, approve, digest)
		}
		return e
	})
	return
}

type MessagePage struct {
	Messages []agentcontext.Message `json:"messages"`
	Next     *int64                 `json:"next_before_sequence"`
}

func (s *Service) MessageHistory(ctx context.Context, user, id uuid.UUID, before int64) (MessagePage, error) {
	scope, _, e := s.scope(ctx, user)
	if e != nil {
		return MessagePage{}, e
	}
	pager, ok := s.ContextRepo.(interface {
		Page(context.Context, uuid.UUID, string, uuid.UUID, int64) (MessagePage, error)
	})
	if !ok {
		return MessagePage{}, ErrNotFound
	}
	return pager.Page(ctx, user, scope, id, before)
}

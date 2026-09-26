package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/mutationlock"
	"github.com/google/uuid"
)

type queryExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type transactionKey struct{}
type transactionBinding struct {
	pool *sql.DB
	tx   *sql.Tx
}

// repositoryDatabase keeps ordinary requests on the pool, but routes every
// repository call in an approved Agent action to its one transaction/connection.
// The binding is request-local; shared repository instances are never modified.
type repositoryDatabase struct{ pool *sql.DB }

func newRepositoryDatabase(pool *sql.DB) *repositoryDatabase {
	return &repositoryDatabase{pool: pool}
}

func (d *repositoryDatabase) executor(ctx context.Context) queryExecutor {
	if binding, ok := ctx.Value(transactionKey{}).(transactionBinding); ok && binding.pool == d.pool {
		return binding.tx
	}
	return d.pool
}

func (d *repositoryDatabase) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.executor(ctx).ExecContext(ctx, query, args...)
}

func (d *repositoryDatabase) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.executor(ctx).QueryContext(ctx, query, args...)
}

func (d *repositoryDatabase) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.executor(ctx).QueryRowContext(ctx, query, args...)
}

// Existing repository methods may declare their own transaction boundaries.
// Inside an Agent transaction these become savepoints: their Commit cannot
// publish a business change before the Agent action receipt is saved.
func (d *repositoryDatabase) BeginTx(ctx context.Context, options *sql.TxOptions) (*repositoryTransaction, error) {
	if binding, ok := ctx.Value(transactionKey{}).(transactionBinding); ok && binding.pool == d.pool {
		name := "repository_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err := binding.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
			return nil, err
		}
		return &repositoryTransaction{Tx: binding.tx, ctx: ctx, savepoint: name}, nil
	}
	tx, err := d.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &repositoryTransaction{Tx: tx, ctx: ctx}, nil
}

type repositoryTransaction struct {
	*sql.Tx
	ctx       context.Context
	savepoint string
	closed    bool
}

func (t *repositoryTransaction) Commit() error {
	if t.closed {
		return sql.ErrTxDone
	}
	if t.savepoint == "" {
		t.closed = true
		return t.Tx.Commit()
	}
	_, err := t.Tx.ExecContext(t.ctx, "RELEASE SAVEPOINT "+t.savepoint)
	if err == nil {
		t.closed = true
	}
	return err
}

func (t *repositoryTransaction) Rollback() error {
	if t.closed {
		return sql.ErrTxDone
	}
	t.closed = true
	if t.savepoint == "" {
		return t.Tx.Rollback()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), 5*time.Second)
	defer cancel()
	if _, err := t.Tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+t.savepoint); err != nil {
		return err
	}
	_, err := t.Tx.ExecContext(ctx, "RELEASE SAVEPOINT "+t.savepoint)
	return err
}

func (r *AgentRepository) WithinUserTransaction(ctx context.Context, user uuid.UUID, run func(context.Context) error) error {
	if _, bound := ctx.Value(transactionKey{}).(transactionBinding); bound {
		return errors.New("Agent action transaction is already active")
	}
	tx, err := beginUserActionTransaction(ctx, r.db.pool, user)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	txCtx := context.WithValue(ctx, transactionKey{}, transactionBinding{pool: r.db.pool, tx: tx})
	if err := run(txCtx); err != nil {
		return err
	}
	// This commits both business data and the action receipt, and releases the
	// account advisory lock. Waiting for user approval and Eino's surrounding
	// chat/model calls are outside this transaction.
	return tx.Commit()
}

// Page writers hold their account lock on a separate pool and then need the
// business pool for their writes. Blocking on the same lock while occupying
// business connections would deadlock when concurrent confirmations fill that
// pool. Try the transaction lock without waiting; roll back and release the
// connection between attempts. Once acquired, it lasts through the one COMMIT.
func beginUserActionTransaction(ctx context.Context, pool *sql.DB, user uuid.UUID) (*sql.Tx, error) {
	for {
		tx, err := pool.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		var locked bool
		if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, mutationlock.Key(user)).Scan(&locked); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if locked {
			return tx, nil
		}
		if err := tx.Rollback(); err != nil {
			return nil, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *AgentRepository) WithinSavepoint(ctx context.Context, run func(context.Context) error) error {
	if binding, ok := ctx.Value(transactionKey{}).(transactionBinding); !ok || binding.pool != r.db.pool {
		return errors.New("business savepoint requires the Agent action transaction")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := run(ctx); err != nil {
		// Roll back partial business writes, but leave the outer transaction
		// usable so it can persist the failed action receipt.
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

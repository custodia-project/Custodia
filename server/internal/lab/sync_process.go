package lab

import (
	"context"
	"fmt"
	"time"

	"custodia/server/internal/adapters/httpinventory"
	inventorysync "custodia/server/internal/sync"
)

// ProcessSyncRun claims one queued run and publishes its result. The
// laboratory executes the extraction synchronously, but the durable queued run
// and its audit exist before the first external request, exactly like the
// Python coordinator contract. A future worker can call this method without
// changing the storage or authorization rules.
func (s *Store) ProcessSyncRun(ctx context.Context, runID, workerID string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	// Terminal publication must survive a cancelled fetch: the run history is
	// the durable evidence and must never stay "running" only because the
	// request that triggered it disconnected.
	finishCtx := context.WithoutCancel(ctx)
	run, err := s.claimSyncRun(ctx, runID, workerID, now())
	if err != nil {
		return err
	}
	attempt := run.AttemptCount
	source, err := s.loadSyncSource(ctx, run.ConnectionID)
	if err != nil || !source.Enabled || source.Collection != run.Collection ||
		source.Scope != run.Scope || source.Generation != run.Generation || source.BaseURL != run.SourceURL {
		return s.finishSyncRun(finishCtx, runID, workerID, attempt, syncResult{
			Status: "failed", ErrorCode: "configuration", ErrorDetail: "sync source is not available",
		}, now())
	}
	client, err := httpinventory.New(source.BaseURL, httpinventory.Options{})
	if err != nil {
		return s.finishSyncRun(finishCtx, runID, workerID, attempt, syncResult{
			Status: "failed", ErrorCode: "configuration", ErrorDetail: "sync source configuration is invalid",
		}, now())
	}
	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var progressErr error
	outcome := inventorysync.Fetch(fetchCtx, client, run.Collection, inventorysync.Limits{
		PageSize: source.PageSize, MaxPages: source.MaxPages, MaxRecords: source.MaxRecords,
	}, func(page, received int) {
		if progressErr != nil {
			return
		}
		if err := s.progressSyncRun(ctx, runID, workerID, attempt, fmt.Sprintf("Página %d · %d registros", page, received), now()); err != nil {
			progressErr = err
			cancel()
		}
	})
	// Always terminalize the run, even if a heartbeat failed. If the attempt
	// lost its lease, the guarded finish returns ErrSyncConflict and the new
	// owner is left untouched.
	finishErr := s.finishSyncRun(finishCtx, runID, workerID, attempt, syncResult{
		Status: outcome.Status, Objects: outcome.Objects, Pages: outcome.Pages, Received: outcome.Received,
		Duplicates: outcome.Duplicates, ErrorCode: outcome.ErrorCode, ErrorDetail: outcome.ErrorDetail,
		Warnings: outcome.Warnings,
	}, now())
	if progressErr != nil {
		return progressErr
	}
	return finishErr
}

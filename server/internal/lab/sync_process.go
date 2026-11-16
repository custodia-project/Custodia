package lab

import (
	"context"
	"fmt"
	"time"

	"custodia/server/internal/adapters/cyberarkpam"
	"custodia/server/internal/adapters/httpinventory"
	inventorysync "custodia/server/internal/sync"
)

// ProcessSyncRun claims one queued run and publishes its result. The worker
// loop uses ProcessNextSyncRun; this entry point is also used by tests and by
// operators resuming a specific queued run.
func (s *Store) ProcessSyncRun(ctx context.Context, runID, workerID string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	run, err := s.claimSyncRun(ctx, runID, workerID, now())
	if err != nil {
		return err
	}
	return s.processClaimedSyncRun(ctx, run, workerID, now)
}

// processClaimedSyncRun fetches and publishes one already-claimed attempt. It
// always terminalizes the run unless the attempt lost its lease; in that case
// the guarded finish returns ErrSyncConflict and the new owner is untouched.
func (s *Store) processClaimedSyncRun(ctx context.Context, run SyncRun, workerID string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	finishCtx := context.WithoutCancel(ctx)
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go s.heartbeatLoop(heartbeatCtx, run.RunID, workerID, run.AttemptCount, now)
	source, err := s.loadSyncSource(ctx, run.ConnectionID)
	if err != nil || !source.Enabled || source.Collection != run.Collection ||
		source.Scope != run.Scope || source.Generation != run.Generation || source.BaseURL != run.SourceURL {
		return s.finishSyncRun(finishCtx, run.RunID, workerID, run.AttemptCount, syncResult{
			Status: "failed", ErrorCode: "configuration", ErrorDetail: "sync source is not available",
		}, now())
	}
	client, err := newSyncAdapter(source)
	if err != nil {
		return s.finishSyncRun(finishCtx, run.RunID, workerID, run.AttemptCount, syncResult{
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
		if err := s.progressSyncRun(ctx, run.RunID, workerID, run.AttemptCount, fmt.Sprintf("Página %d · %d registros", page, received), now()); err != nil {
			progressErr = err
			cancel()
		}
	})
	finishErr := s.finishSyncRun(finishCtx, run.RunID, workerID, run.AttemptCount, syncResult{
		Status: outcome.Status, Objects: outcome.Objects, Pages: outcome.Pages, Received: outcome.Received,
		Duplicates: outcome.Duplicates, ErrorCode: outcome.ErrorCode, ErrorDetail: outcome.ErrorDetail,
		Warnings: outcome.Warnings,
	}, now())
	if progressErr != nil {
		return progressErr
	}
	return finishErr
}

// newSyncAdapter selects the contract from the registered connector. The
// worker never probes a provider or invents an endpoint: an unknown connector
// fails the run before any request.
func newSyncAdapter(source SyncSource) (inventorysync.Source, error) {
	switch source.Connector {
	case syncConnector:
		return httpinventory.New(source.BaseURL, httpinventory.Options{})
	case syncConnectorCyberArkPAMSafes:
		return cyberarkpam.New(source.BaseURL, cyberarkpam.Options{})
	default:
		return nil, fmt.Errorf("unsupported sync connector")
	}
}

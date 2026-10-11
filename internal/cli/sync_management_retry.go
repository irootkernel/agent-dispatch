package cli

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
)

func managementRetry(ctx context.Context) bool {
	e, _ := ctx.Value(managementExecutionKey{}).(*managementExecution)
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.request.Action == "retry"
}

// Recovery helpers see only eligible recorded obligations. Their own durable
// effect inspection and claim fencing still precede every replay.
func managementRetryAllowsJob(ctx context.Context, job sqlite.SyncJobRow) bool {
	e, _ := ctx.Value(managementExecutionKey{}).(*managementExecution)
	if e == nil {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.request.Action != "retry" {
		return true
	}
	j, ok := e.retryJobs[job.JobID]
	if !ok || !j.Eligible || j.Kind != job.Kind || job.Attempts >= syncmanagement.AttemptLimit || sqlite.SyncClaimLiveAt(job.ClaimOwner, job.ClaimExpiresAt, time.Now()) {
		return false
	}
	switch job.Kind {
	case "publication":
		return job.State == "signed" || job.State == "uncertain"
	case "import":
		return job.State == "validated" || job.State == "applying" || job.State == "recovering" || job.State == "uncertain"
	case "delivery":
		return job.State == "pending" || job.State == "retryable" || job.State == "unknown"
	}
	return false
}

func (s *peerService) retryManagement(ctx context.Context, e *managementExecution, client *http.Client, out io.Writer) int {
	content, delivery := false, false
	for _, j := range e.retryJobs {
		if j.Eligible {
			content = content || j.Kind == "import" || j.Kind == "publication"
			delivery = delivery || j.Kind == "delivery"
		}
	}
	if content {
		if code := reconcileConfiguredSync(ctx, s.cfg, s.store, out, io.Discard); code != 0 {
			return code
		}
	}
	if delivery {
		if client == nil {
			client = peerHTTPClient()
			defer client.CloseIdleConnections()
		}
		if err := s.deliverPending(ctx, client); err != nil {
			return 10
		}
	}
	return 0
}

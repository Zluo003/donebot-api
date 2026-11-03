package handler

import (
	"context"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type imageTaskBillingContextKey struct{}

// Hold the existing billing jobs until the asynchronous result is durably
// available. This transfers reservation references, without another ledger.
type imageTaskBilling struct {
	mu     sync.Mutex
	closed bool
	jobs   []imageTaskBillingJob
}

type imageTaskBillingJob struct {
	submit  func()
	abandon func()
}

func (b *imageTaskBilling) hold(parent context.Context, task service.UsageRecordTask, submit func(context.Context, service.UsageRecordTask)) {
	wrapped, abandon := wrapUsageRecordTaskContext(parent, task)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		abandon()
		return
	}
	b.jobs = append(b.jobs, imageTaskBillingJob{submit: func() { submit(context.Background(), wrapped) }, abandon: abandon})
	b.mu.Unlock()
}

func (b *imageTaskBilling) finish(delivered bool) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	jobs := b.jobs
	b.jobs = nil
	b.mu.Unlock()
	for _, job := range jobs {
		if delivered {
			job.submit()
		} else {
			job.abandon()
		}
	}
}

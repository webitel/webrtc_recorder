package service

import (
	"context"
	"time"

	"go.uber.org/atomic"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/infra/metrics"
	"github.com/webitel/webrtc_recorder/internal/model"
)

type jobHandler struct {
	// inflight counts jobs taken from the database and not finished yet (running + waiting in the pool queue)
	inflight atomic.Int32
	name     string
	jobStore FileJobStore
	ctx      context.Context
	log      *wlog.Logger
	tempFile *TempFileService
}

type baseJob struct {
	job *model.Job
	ctx context.Context
	log *wlog.Logger
}

// freeSlots returns how many jobs can be taken from the database without blocking the pool:
// the store marks fetched jobs as active, so fetching more than the pool can accept leaves them
// "active" while they wait for a worker and blocks the listener in pool.Exec.
func (svc *jobHandler) freeSlots(limit int) int {
	return limit - int(svc.inflight.Load())
}

// dispatched must be called for each job handed to the worker pool.
func (svc *jobHandler) dispatched() {
	svc.inflight.Inc()
	metrics.JobsDispatched.WithLabelValues(svc.name).Inc()
}

// startJob tracks a job attempt, the returned func must be called with the attempt result.
func (svc *jobHandler) startJob(j *baseJob, maxRetry int) func(err error) {
	start := time.Now()
	metrics.JobsInProgress.WithLabelValues(svc.name).Inc()

	return func(err error) {
		svc.inflight.Dec()
		metrics.JobsInProgress.WithLabelValues(svc.name).Dec()
		metrics.JobsDispatched.WithLabelValues(svc.name).Dec()

		result := metrics.ResultSuccess

		switch {
		case err != nil && j.job.Retry >= maxRetry:
			result = metrics.ResultDropped
		case err != nil:
			result = metrics.ResultError
		default:
			metrics.JobAttempt.WithLabelValues(svc.name).Observe(float64(j.job.Retry))
		}

		metrics.JobsTotal.WithLabelValues(svc.name, result).Inc()
		metrics.JobDuration.WithLabelValues(svc.name, result).Observe(time.Since(start).Seconds())
	}
}

func (svc *jobHandler) errorJob(j *baseJob, maxRetry int, err error) {
	j.log.Error(err.Error(), wlog.Err(err))

	if j.job.Retry >= maxRetry {
		j.log.Error("max attempts reached")
		svc.cleanup(j)

		return
	}

	err = svc.jobStore.SetError(j.job.ID, err)
	if err != nil {
		j.log.Error(err.Error(), wlog.Err(err))
	}
}

func (svc *jobHandler) cleanup(j *baseJob) {
	err := svc.tempFile.DeleteFile(j.job.File)
	if err != nil {
		j.log.Error(err.Error(), wlog.Err(err))
	}

	err = svc.jobStore.Delete(j.job.ID)
	if err != nil {
		j.log.Error(err.Error(), wlog.Err(err))
	}
}

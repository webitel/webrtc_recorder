package service

import (
	"context"

	"go.uber.org/atomic"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/internal/model"
)

type jobHandler struct {
	// inflight counts jobs taken from the database and not finished yet (running + waiting in the pool queue)
	inflight atomic.Int32
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

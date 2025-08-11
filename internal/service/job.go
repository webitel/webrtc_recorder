package service

import (
	"context"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/internal/model"
)

type jobHandler struct {
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

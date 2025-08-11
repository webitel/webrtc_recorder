package service

import (
	"context"
	"time"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/config"
	"github.com/webitel/webrtc_recorder/internal/model"
	"github.com/webitel/webrtc_recorder/internal/utils"
)

const TranscodingJobName = "transcoding"

type FileJobStore interface {
	Create(jobType string, cfg *model.JobConfig, f *model.File) error
	Update(state model.JobState, j *model.Job) error
	SetError(id int, err error) error
	Fetch(limit int, jobType string) ([]*model.Job, error)
	Delete(id int) error
}

type Transcoding struct {
	jobHandler
	limit    int
	maxRetry int
	pool     *utils.Pool
	uploader *Uploader
}

type transcodingJob struct {
	svc *Transcoding
	*baseJob
}

func NewTranscoding(ctx context.Context, cfg *config.Config, log *wlog.Logger, fjs FileJobStore, tmp *TempFileService, upl *Uploader) *Transcoding {
	tr := &Transcoding{
		jobHandler: jobHandler{
			ctx:      ctx,
			jobStore: fjs,
			log:      log,
			tempFile: tmp,
		},
		uploader: upl,
		maxRetry: cfg.Transcoding.MaxRetry,
		limit:    cfg.Transcoding.Queue + cfg.Transcoding.Workers,
		pool:     utils.NewPool(ctx, cfg.Transcoding.Workers, cfg.Transcoding.Queue),
	}

	go tr.listen()

	return tr
}

func (svc *Transcoding) CreateJob(f *model.File) error {
	return svc.jobStore.Create(TranscodingJobName, &model.JobConfig{}, f)
}

func (svc *Transcoding) successJob(j *transcodingJob, trFile *model.File) {
	var err error

	if err = svc.tempFile.DeleteFile(j.job.File); err != nil {
		j.log.Error(err.Error(), wlog.Err(err))
	}

	uploadJob := *j.job
	uploadJob.File = trFile

	uploadJob.Type = UploadJobName
	uploadJob.Retry = 0
	err = svc.jobStore.Update(model.JobIdle, &uploadJob)
	if err != nil {
		j.log.Error(err.Error(), wlog.Err(err))
	}
}

func (svc *Transcoding) listen() {
	svc.log.Debug("listening for transcoding jobs")

	ticker := time.NewTicker(time.Second)

	defer func() {
		ticker.Stop()
		svc.pool.Close()
		svc.log.Debug("transcoding listener closed")
	}()

	for {
		select {
		case <-svc.ctx.Done():
			return
		case <-ticker.C:
			jobs, err := svc.jobStore.Fetch(svc.limit, TranscodingJobName)
			if err != nil {
				svc.log.Error(err.Error(), wlog.Err(err))
				time.Sleep(time.Second)
				continue
			}
			for _, job := range jobs {
				job.Retry++
				svc.pool.Exec(&transcodingJob{
					svc: svc,
					baseJob: &baseJob{
						job: job,
						ctx: svc.ctx,
						log: svc.log.With(wlog.Int("job_id", job.Id), wlog.String("job_type", job.Type),
							wlog.Int("attempt", job.Retry)),
					},
				})
			}
		}
	}
}

func (j *transcodingJob) Execute() {
	j.log.Debug("execute")
	var err error
	mp4File := *j.job.File
	mp4File.Path = ""
	mp4File.MimeType = "video/mp4" // TODO

	now := time.Now()

	defer func() {
		if err != nil {
			j.svc.errorJob(j.baseJob, j.svc.maxRetry, err)
		} else {
			j.log.Debug("success job", wlog.Duration("duration", time.Since(now)))
			j.svc.successJob(j, &mp4File)
		}
	}()

	err = j.svc.tempFile.NewFilePath(&mp4File, "mp4")
	if err != nil {
		return
	}

	err = utils.TranscodingByPath(j.job.File.Path, mp4File.Path)
	if err != nil {
		return
	}
}

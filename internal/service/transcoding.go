package service

import (
	"context"
	"github.com/webitel/webrtc_recorder/config"
	"github.com/webitel/webrtc_recorder/internal/model"
	"github.com/webitel/webrtc_recorder/internal/utils"
	"github.com/webitel/wlog"
	"io"
	"time"
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

func NewTranscoding(ctx context.Context, cfg *config.Config, log *wlog.Logger, fjs FileJobStore, cache *CacheService, upl *Uploader) *Transcoding {
	tr := &Transcoding{
		jobHandler: jobHandler{
			ctx:       ctx,
			jobStore:  fjs,
			log:       log,
			fileCache: cache,
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
	j.log.Debug("success job")

	if err = svc.fileCache.DeleteFile(j.job.File); err != nil {
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
	var src io.ReadCloser
	var dst io.WriteCloser
	var err error
	mp4File := *j.job.File
	mp4File.Path = ""
	mp4File.MimeType = "video/mp4" // TODO

	defer func() {
		if err != nil {
			j.svc.errorJob(j.baseJob, j.svc.maxRetry, err)
		} else {
			j.svc.successJob(j, &mp4File)
		}
	}()

	src, err = j.svc.fileCache.NewReader(*j.job.File)
	if err != nil {
		return
	}
	defer src.Close()

	dst, err = j.svc.fileCache.NewWriter(&mp4File, "mp4")
	if err != nil {
		return
	}
	defer dst.Close()

	tr, err := utils.NewTranscoding(src, dst)
	if err != nil {
		return
	}

	err = tr.Start()
	if err != nil {
		return
	}

	err = tr.Wait()
	if err != nil {
		return
	}
}

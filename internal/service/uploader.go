package service

import (
	"context"
	"io"
	"time"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/config"
	spb "github.com/webitel/webrtc_recorder/gen/storage"
	"github.com/webitel/webrtc_recorder/infra/storage"
	"github.com/webitel/webrtc_recorder/internal/utils"
)

const (
	UploadJobName = "upload"
)

type Uploader struct {
	jobHandler

	pool     *utils.Pool
	storage  *storage.Storage
	limit    int
	maxRetry int
}

type UploadJob struct {
	*baseJob

	svc *Uploader
}

func NewUploader(ctx context.Context, cfg *config.Config, log *wlog.Logger, fjs FileJobStore, tmp *TempFileService, st *storage.Storage) *Uploader {
	u := &Uploader{
		jobHandler: jobHandler{
			log:      log.With(wlog.String("service", "uploader")),
			tempFile: tmp,
			jobStore: fjs,
			ctx:      ctx,
		},
		storage:  st,
		maxRetry: cfg.Uploader.MaxRetry,
		limit:    cfg.Uploader.Queue + cfg.Uploader.Workers,
		pool:     utils.NewPool(ctx, cfg.Uploader.Workers, cfg.Uploader.Queue),
	}

	go u.listen()

	return u
}

func (svc *Uploader) listen() {
	svc.log.Debug("listening for upload jobs")

	ticker := time.NewTicker(time.Second)

	defer func() {
		ticker.Stop()
		svc.pool.Close()
		svc.log.Debug("upload listener closed")
	}()

	for {
		select {
		case <-svc.ctx.Done():
			return
		case <-ticker.C:
			jobs, err := svc.jobStore.Fetch(svc.limit, UploadJobName)
			if err != nil {
				svc.log.Error(err.Error(), wlog.Err(err))
				time.Sleep(time.Second)

				continue
			}

			for _, job := range jobs {
				job.Retry++
				svc.pool.Exec(&UploadJob{
					svc: svc,
					baseJob: &baseJob{
						job: job,
						ctx: svc.ctx,
						log: svc.log.With(wlog.Int("job_id", job.ID), wlog.String("job_type", job.Type),
							wlog.Int("attempt", job.Retry)),
					},
				})
			}
		}
	}
}

func (j *UploadJob) Execute() {
	var (
		err error
		src io.ReadCloser
	)

	now := time.Now()

	j.log.Debug("execute")

	defer func() {
		if err != nil {
			j.svc.errorJob(j.baseJob, j.svc.maxRetry, err)
		} else {
			j.log.Debug("success job", wlog.Duration("duration", time.Since(now)))
			j.svc.cleanup(j.baseJob)
		}
	}()

	src, err = j.svc.tempFile.NewReader(*j.job.File)
	if err != nil {
		return
	}
	defer src.Close()

	stream, err := j.svc.storage.API().UploadFile(j.ctx)
	if err != nil {
		return
	}

	err = stream.Send(&spb.UploadFileRequest{
		Data: &spb.UploadFileRequest_Metadata_{
			Metadata: &spb.UploadFileRequest_Metadata{
				DomainId:          int64(j.job.File.DomainID),
				Name:              j.job.File.Name,
				MimeType:          j.job.File.MimeType,
				Uuid:              j.job.File.UUID,
				CreatedAt:         int64(j.job.File.CreatedAt),
				StreamResponse:    false,
				Channel:           spb.UploadFileChannel(j.job.File.Channel),
				GenerateThumbnail: true,
				UploadedBy:        int64(j.job.File.UploadedBy),
			},
		},
	})
	if err != nil {
		return
	}

	buf := make([]byte, 1024*256)

	var n int
	for {
		n, err = src.Read(buf)
		if err == io.EOF {
			err = nil
		}

		if n == 0 {
			stream.CloseSend()

			break
		}

		err = stream.Send(&spb.UploadFileRequest{
			Data: &spb.UploadFileRequest_Chunk{
				Chunk: buf[:n],
			},
		})
		if err != nil {
			break
		}
	}
}

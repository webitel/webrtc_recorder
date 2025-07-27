package store

import (
	"context"
	"github.com/webitel/webrtc_recorder/config"
	"github.com/webitel/webrtc_recorder/infra/sql"
	"github.com/webitel/webrtc_recorder/internal/model"
	"github.com/webitel/wlog"
)

type FileJobStore struct {
	db       sql.Store
	ctx      context.Context
	instance string
	log      *wlog.Logger
}

func NewFileJobStore(ctx context.Context, log *wlog.Logger, cfg *config.Config, db sql.Store) *FileJobStore {
	fjs := &FileJobStore{
		db:       db,
		ctx:      ctx,
		instance: cfg.Service.Id,
		log:      log.With(wlog.String("store", "file_jobs")),
	}

	err := fjs.Reset()
	if err != nil {
		fjs.log.Error(err.Error(), wlog.Err(err))
	}

	return fjs
}

func (s *FileJobStore) Create(jobType string, cfg *model.JobConfig, f *model.File) error {
	return s.db.Exec(s.ctx, `insert into webrtc_rec.file_jobs (type, instance, file, config)
values (@type, @instance, @file, @config)`, map[string]any{
		"type":     jobType,
		"instance": s.instance,
		"file":     f.Json(),
		"config":   cfg.Json(),
	})
}

func (s *FileJobStore) Update(state model.JobState, j *model.Job) error {
	return s.db.Exec(s.ctx, `update webrtc_rec.file_jobs
set state = @state,
    type = @type,
    file = @file,
    config = @config,
    error = @error,
    retry = @retry,
    activity_at = now()
where id = @id`, map[string]any{
		"id":     j.Id,
		"state":  state,
		"type":   j.Type,
		"file":   j.File.Json(),
		"config": j.Config.Json(),
		"error":  nil, // TODO
		"retry":  j.Retry,
	})
}

func (s *FileJobStore) Reset() error {
	return s.db.Exec(s.ctx, `update webrtc_rec.file_jobs
set state = @state
where instance = @instance;`, map[string]any{
		"instance": s.instance,
		"state":    model.JobIdle,
	})
}

func (s *FileJobStore) Fetch(limit int, jobType string) ([]*model.Job, error) {
	var jobs []*model.Job
	err := s.db.Select(s.ctx, &jobs, `update webrtc_rec.file_jobs j
set state = @state,
    activity_at = now(),
    retry = j.retry + 1
from (
    select id, type, file, config, retry
    from webrtc_rec.file_jobs
    where state = 0
        and instance = @instance
    	and type = @type
    order by created_at
    limit @limit
) x 
where x.id = j.id
returning x.*`, map[string]any{
		"limit":    limit,
		"type":     jobType,
		"instance": s.instance,
		"state":    model.JobActive,
	})

	if err != nil {
		return nil, err
	}

	return jobs, nil
}

func (s *FileJobStore) SetError(id int, err error) error {
	return s.db.Exec(s.ctx, `update webrtc_rec.file_jobs
set error = @error,
    state = @state,
    activity_at = now()
where id = @id`, map[string]any{
		"id":    id,
		"error": err.Error(),
		"state": model.JobIdle,
	})
}

func (s *FileJobStore) Delete(id int) error {
	return s.db.Exec(s.ctx, `delete
from webrtc_rec.file_jobs
where id = @id`, map[string]any{
		"id": id,
	})
}

package service

import (
	"context"
	"time"

	"golang.org/x/sys/unix"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/infra/metrics"
	"github.com/webitel/webrtc_recorder/internal/model"
)

const statsInterval = 15 * time.Second

type JobStatsStore interface {
	Stats() ([]*model.JobStats, error)
}

// StatsCollector periodically refreshes metrics that can't be tracked by events: job backlog and temp dir disk usage.
type StatsCollector struct {
	ctx   context.Context
	log   *wlog.Logger
	store JobStatsStore
	tmp   *TempFileService
}

func NewStatsCollector(ctx context.Context, log *wlog.Logger, store JobStatsStore, tmp *TempFileService) *StatsCollector {
	c := &StatsCollector{
		ctx:   ctx,
		log:   log.With(wlog.String("service", "stats")),
		store: store,
		tmp:   tmp,
	}

	go c.listen()

	return c
}

func (c *StatsCollector) listen() {
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()

	for {
		c.collect()

		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *StatsCollector) collect() {
	var st unix.Statfs_t
	if err := unix.Statfs(c.tmp.Dir(), &st); err != nil {
		c.log.Error(err.Error(), wlog.Err(err))
	} else {
		metrics.TempDirFree.Set(float64(uint64(st.Bavail) * uint64(st.Bsize))) //nolint:unconvert
		metrics.TempDirSize.Set(float64(uint64(st.Blocks) * uint64(st.Bsize))) //nolint:unconvert
	}

	stats, err := c.store.Stats()
	if err != nil {
		c.log.Error(err.Error(), wlog.Err(err))

		return
	}

	// known types are always reported, so a drained backlog shows zero instead of the last seen value
	backlog := make(map[[2]string]int)
	oldest := make(map[string]float64)

	for _, t := range []string{TranscodingJobName, UploadJobName} {
		backlog[[2]string{t, "idle"}] = 0
		backlog[[2]string{t, "active"}] = 0
		oldest[t] = 0
	}

	for _, s := range stats {
		state := "idle"
		if s.State == model.JobActive {
			state = "active"
		}

		backlog[[2]string{s.Type, state}] += s.Count
		oldest[s.Type] = max(oldest[s.Type], s.OldestAge)
	}

	for k, v := range backlog {
		metrics.JobsBacklog.WithLabelValues(k[0], k[1]).Set(float64(v))
	}

	for t, age := range oldest {
		metrics.JobsOldestAge.WithLabelValues(t).Set(age)
	}
}

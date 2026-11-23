package metrics

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "webrtc_recorder"

const (
	ResultSuccess = "success"
	ResultError   = "error"   // attempt failed, job will be retried
	ResultDropped = "dropped" // max attempts reached, recording is lost
)

var (
	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "jobs_total",
		Help:      "Finished job attempts by type and result.",
	}, []string{"type", "result"})

	JobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "job_duration_seconds",
		Help:      "Job attempt execution time.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600, 1200, 3600},
	}, []string{"type", "result"})

	JobAttempt = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "job_attempt",
		Help:      "Attempt number at which a job finished successfully.",
		Buckets:   []float64{1, 2, 3, 5, 10, 20},
	}, []string{"type"})

	JobsInProgress = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "jobs_in_progress",
		Help:      "Jobs being executed by workers right now.",
	}, []string{"type"})

	JobsDispatched = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "jobs_dispatched",
		Help:      "Jobs taken from the database and not finished yet (in progress + waiting in the in-memory queue).",
	}, []string{"type"})

	Workers = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "workers",
		Help:      "Configured worker count.",
	}, []string{"type"})

	JobsBacklog = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "jobs_backlog",
		Help:      "Jobs stored in the database for this instance by type and state (idle|active).",
	}, []string{"type", "state"})

	JobsOldestAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "jobs_oldest_age_seconds",
		Help:      "Age of the oldest job stored in the database for this instance.",
	}, []string{"type"})

	TranscodingMediaSeconds = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "transcoding_media_seconds_total",
		Help:      "Duration of successfully transcoded media.",
	})

	TranscodingSpeed = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "transcoding_speed_ratio",
		Help:      "Media duration divided by transcoding time (>1 is faster than realtime).",
		Buckets:   []float64{0.25, 0.5, 1, 2, 4, 8, 16, 32, 64},
	})

	UploadBytes = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "upload_bytes_total",
		Help:      "Bytes sent to the storage service.",
	})

	UploadFileSize = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "upload_file_size_bytes",
		Help:      "Size of successfully uploaded files.",
		Buckets:   prometheus.ExponentialBuckets(256*1024, 4, 9), // 256KiB .. 16GiB
	})

	SessionsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sessions_active",
		Help:      "Active WebRTC recording sessions.",
	})

	SessionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "sessions_total",
		Help:      "Finished WebRTC sessions by final ICE state (connected|failed|closed|...) and kind (record|diagnostic).",
	}, []string{"ice_state", "kind"})

	SessionDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "session_duration_seconds",
		Help:      "WebRTC session duration.",
		Buckets:   []float64{5, 15, 30, 60, 300, 600, 1800, 3600, 7200, 14400},
	})

	SessionICEConnect = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "session_ice_connect_seconds",
		Help:      "Time from session creation to the first ICE connected.",
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
	})

	SessionICEDisconnects = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "session_ice_disconnects_total",
		Help:      "ICE disconnected transitions.",
	})

	SessionRTT = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "session_rtt_seconds",
		Help:      "Round trip time of the selected ICE candidate pair at session end.",
		Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1},
	})

	SessionCandidatePair = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "session_candidate_pair_total",
		Help:      "Selected ICE candidate pair types (relay means traffic goes through TURN).",
	}, []string{"local", "remote", "protocol"})

	RTPPackets = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "rtp_packets_total",
		Help:      "Inbound RTP packets by media kind and state (received|lost|unrecovered|late).",
	}, []string{"kind", "state"})

	RTPFeedback = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "rtp_feedback_total",
		Help:      "RTCP feedback sent to clients by media kind and type (nack|pli).",
	}, []string{"kind", "type"})

	RTPBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "rtp_bytes_total",
		Help:      "Inbound RTP bytes by media kind.",
	}, []string{"kind"})

	TempDirFree = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "temp_dir_free_bytes",
		Help:      "Free space available on the temp (cache) dir filesystem.",
	})

	TempDirSize = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "temp_dir_size_bytes",
		Help:      "Total size of the temp (cache) dir filesystem.",
	})
)

// Server exposes /metrics over HTTP.
type Server struct {
	srv *http.Server
	ln  net.Listener
}

func NewServer(addr string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	return &Server{
		ln: ln,
		srv: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}, nil
}

func (s *Server) Addr() string {
	return s.ln.Addr().String()
}

func (s *Server) Listen() error {
	if err := s.srv.Serve(s.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.srv.Shutdown(ctx)
}

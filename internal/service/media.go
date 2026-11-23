package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/h264writer"
	"github.com/pion/webrtc/v4/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
	"go.uber.org/atomic"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/infra/metrics"
	"github.com/webitel/webrtc_recorder/internal/model"
)

// maxLatePackets is the samplebuilder window: a screen keyframe can span 100+ packets,
// and NACK retransmits must still fit into the window to avoid corrupted frames.
const maxLatePackets = 256

type Track struct {
	model.File
	countPkg int
	writer   io.WriteCloser `json:"-"`
	encoder  media.Writer   `json:"-"`

	lostPackets atomic.Uint32 // gaps after samplebuilder window (not recovered by NACK)
	latePackets atomic.Uint32 // released out of order
}

type RtcUploadMediaSession struct {
	id         string
	answer     *webrtc.SessionDescription
	offer      webrtc.SessionDescription
	pc         *webrtc.PeerConnection
	log        *wlog.Logger
	fileConfig *model.File
	cancel     context.CancelFunc
	ctx        context.Context
	rec        *WebRtcRecorder
	track      []*Track
	tmp        *TempFileService
	countTrack atomic.Int32

	mu     sync.Mutex // guards track, fileConfig and closed
	closed bool

	createdAt time.Time
	statsOnce sync.Once

	iceConnectMs   atomic.Int64 // time from session creation to the first ICE connected, 0 if never
	iceDisconnects atomic.Int32
}

func NewWebRtcUploadSession(rec *WebRtcRecorder, pc *webrtc.PeerConnection, file *model.File) *RtcUploadMediaSession {
	id := model.NewID()
	session := &RtcUploadMediaSession{
		id:         id,
		fileConfig: file,
		rec:        rec,
		pc:         pc,
		log:        rec.log.With(wlog.String("session", id)),
		track:      make([]*Track, 0, 2),
		createdAt:  time.Now(),
	}

	session.ctx, session.cancel = context.WithCancel(context.Background())
	pc.OnTrack(session.onTrack)
	pc.OnDataChannel(session.onDataChannel)
	pc.OnICEConnectionStateChange(session.onICEConnectionStateChange)

	return session
}

func (s *RtcUploadMediaSession) onDataChannel(dataChannel *webrtc.DataChannel) {
	s.log.Debug(fmt.Sprintf("new DataChannel %s %d", dataChannel.Label(), dataChannel.ID()))
	t := Track{
		File:    *s.fileConfig,
		writer:  nil,
		encoder: nil,
	}
	var err error

	t.writer, err = s.rec.temp.NewWriter(&t.File, "raw")
	if err != nil {
		// TODO
		s.log.Error(err.Error(), wlog.Err(err))
		return
	}
	t.Path = t.File.Path

	s.mu.Lock()
	s.track = append(s.track, &t)
	s.fileConfig.Track = append(s.fileConfig.Track, model.MediaChannel{
		Path:     t.Path,
		MimeType: dataChannel.Label(),
	})
	s.mu.Unlock()

	s.countTrack.Add(1)

	dataChannel.OnClose(func() {
		s.log.Debug(fmt.Sprintf("close DataChannel %s", dataChannel.Label()))
		s.countTrack.Add(-1)
		s.close()
	})

	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		t.writer.Write(msg.Data)
	})
}

func (s *RtcUploadMediaSession) onTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	codec := track.Codec()
	t := Track{
		File:    *s.fileConfig,
		writer:  nil,
		encoder: nil,
	}

	var (
		err error
		pkt rtp.Depacketizer
	)

	t.writer, err = s.rec.temp.NewWriter(&t.File, "raw")
	if err != nil {
		// TODO
		s.log.Error(err.Error(), wlog.Err(err))
		return
	}
	s.mu.Lock()
	s.track = append(s.track, &t)
	s.fileConfig.Track = append(s.fileConfig.Track, model.MediaChannel{
		Path:     t.Path,
		MimeType: codec.MimeType,
	})
	trackIdx := len(s.fileConfig.Track) - 1
	if s.fileConfig.StartTime == 0 {
		s.fileConfig.StartTime = model.GetMillis()
	}
	s.mu.Unlock()

	s.log.Debug(fmt.Sprintf("got %s: %s@%d track, saving as %s", track.ID(), codec.MimeType, codec.ClockRate, t.Path))

	s.countTrack.Add(1)

	var firstRtpTs, lastRtpTs uint32

	defer func() {
		if firstRtpTs != 0 {
			s.mu.Lock()
			s.fileConfig.Track[trackIdx].FirstRtpTs = firstRtpTs
			s.fileConfig.Track[trackIdx].LastRtpTs = lastRtpTs
			s.mu.Unlock()
		}

		s.countTrack.Add(-1)
		s.log.Debug("closing writer")
		s.close()
	}()

	switch codec.MimeType {
	case webrtc.MimeTypeVP9:
		t.MimeType = codec.MimeType
		pkt = &codecs.VP9Packet{}

		t.encoder, err = ivfwriter.NewWith(t.writer, ivfwriter.WithCodec(codec.MimeType))
		if err != nil {
			s.log.Error(fmt.Sprintf("failed to open ivf file: %s", err))

			return
		}

	case webrtc.MimeTypeH264:
		t.MimeType = codec.MimeType
		pkt = &codecs.H264Packet{}
		t.encoder = h264writer.NewWith(t.writer)
	case webrtc.MimeTypeOpus:
		t.MimeType = codec.MimeType
		pkt = &codecs.OpusPacket{}
		t.encoder, _ = oggwriter.NewWith(t.writer, codec.ClockRate, codec.Channels)

	default:
		s.log.Error(fmt.Sprintf("unsupported codec: %s", codec.MimeType))
		return // TODO
	}

	if t.encoder != nil {
		var (
			rtpPacket *rtp.Packet
			sample    *media.Sample
			lsn       uint16 = 0
			isVideo          = strings.HasPrefix(codec.MimeType, "video")
		)

		builder := samplebuilder.New(maxLatePackets, pkt, codec.ClockRate,
			samplebuilder.WithRTPHeaders(true),
			samplebuilder.WithPacketReleaseHandler(func(pkt *rtp.Packet) {
				if lsn != 0 && pkt.SequenceNumber != lsn+1 {
					// int16 handles seq wrap-around; negative value means a late (reordered) packet
					// per-packet logs are debug only, totals are reported once in logStats
					if diff := int16(pkt.SequenceNumber - lsn); diff > 1 {
						t.lostPackets.Add(uint32(diff - 1))
						s.log.Debug(fmt.Sprintf("lost packets packet seq=%d, last=%d, count=%d", pkt.SequenceNumber,
							lsn, diff-1))
					} else {
						t.latePackets.Inc()
						s.log.Debug(fmt.Sprintf("late packet seq=%d, last=%d", pkt.SequenceNumber, lsn))
					}
				}

				lsn = pkt.SequenceNumber

				if isVideo {
					if firstRtpTs == 0 {
						firstRtpTs = pkt.Timestamp
					}
					lastRtpTs = pkt.Timestamp
				}

				if err = t.encoder.WriteRTP(pkt); err != nil {
					s.log.Error(fmt.Sprintf("failed to write rtp packet: %s", err))
					s.cancel()
				}
			}),
		)

		for {
			select {
			case <-s.ctx.Done():
				s.log.Debug("context canceled, stopping rtp reader loop")

				return
			default:
				rtpPacket, _, err = track.ReadRTP()
				if err != nil {
					if err != io.EOF {
						s.log.Error(fmt.Sprintf("unhandled error reading rtp packet: %s", err))
					}

					return
				}

				t.countPkg++

				if t.countPkg%1000 == 0 {
					s.log.Debug(fmt.Sprintf("receive rtc (%s) packet count %d", track.ID(), t.countPkg))
				}

				builder.Push(rtpPacket)

				for sample = builder.Pop(); sample != nil; sample = builder.Pop() {
					// if _, err = wd.Write(sample.Data); err != nil {
					//	log.Error(fmt.Sprintf("failed to write rtp packet: %s", err))
					//	cancel()
					//	return
					//}
				}
			}
		}
	}
}

func (s *RtcUploadMediaSession) onICEConnectionStateChange(connectionState webrtc.ICEConnectionState) {
	s.log.Debug(fmt.Sprintf("connection state has changed to %s", connectionState.String()))

	switch connectionState { //nolint:exhaustive
	case webrtc.ICEConnectionStateConnected:
		s.iceConnectMs.CompareAndSwap(0, time.Since(s.createdAt).Milliseconds())
	case webrtc.ICEConnectionStateDisconnected:
		s.iceDisconnects.Inc()
		metrics.SessionICEDisconnects.Inc()
	case webrtc.ICEConnectionStateFailed:
		// closing pc from its own callback may block, so do it asynchronously
		go s.shutdown()
	default:

	}
}

// shutdown stops the session: closing the peer connection unblocks track readers,
// the last one to exit finalizes the session via close.
func (s *RtcUploadMediaSession) shutdown() {
	s.logStats()
	s.cancel()

	if closeErr := s.pc.Close(); closeErr != nil {
		s.log.Error(fmt.Sprintf("closing peer connection: %s", closeErr.Error()))
	}

	s.close()
}

// close finalizes the session once all tracks are done. Safe to call concurrently and repeatedly.
func (s *RtcUploadMediaSession) close() {
	s.log.Debug("close")
	if s.countTrack.Load() > 0 {
		s.log.Debug("wait close track")
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	s.logStats()
	s.cancel()

	for _, track := range s.track {
		// encoder closes the underlying writer itself
		if track.encoder != nil {
			if closeErr := track.encoder.Close(); closeErr != nil {
				s.log.Error(fmt.Sprintf("closing encoder: %s", closeErr.Error()))
			}
		} else if track.writer != nil {
			if closeErr := track.writer.Close(); closeErr != nil {
				s.log.Error(fmt.Sprintf("closing writer: %s", closeErr.Error()))
			}
		}
	}

	// Gracefully shutdown the peer connection
	if closeErr := s.pc.Close(); closeErr != nil {
		s.log.Error(fmt.Sprintf("closing peer connection: %s", closeErr.Error()))
	}

	s.rec.stopVideoSession(s)
}

// logStats writes a single per-session connection quality summary. Must be called before pc is closed.
func (s *RtcUploadMediaSession) logStats() {
	s.statsOnce.Do(func() {
		var (
			report     = s.pc.GetStats()
			pair       *webrtc.ICECandidatePairStats
			candidates = make(map[string]webrtc.ICECandidateStats)
			fields     = []wlog.Field{
				wlog.Duration("duration", time.Since(s.createdAt).Round(time.Second)),
				wlog.Int("domain_id", s.fileConfig.DomainID),
				wlog.Int("user_id", s.fileConfig.UploadedBy),
				wlog.String("ice_state", s.pc.ICEConnectionState().String()),
				wlog.Int64("ice_connect_ms", s.iceConnectMs.Load()),
				wlog.Int("ice_disconnects", int(s.iceDisconnects.Load())),
				wlog.String("offer_candidates", candidateSummary(s.offer.SDP)),
			}
		)

		for _, st := range report {
			switch v := st.(type) {
			case webrtc.ICECandidatePairStats:
				if v.Nominated && v.State == webrtc.StatsICECandidatePairStateSucceeded {
					pair = &v
				}
			case webrtc.ICECandidateStats:
				candidates[v.ID] = v
			case webrtc.InboundRTPStreamStats:
				observeInboundRTP(v)
				fields = append(fields, wlog.Any(v.Kind, map[string]any{
					"packets_received": v.PacketsReceived,
					"packets_lost":     v.PacketsLost,
					"loss_pct":         lossPercent(v.PacketsLost, v.PacketsReceived),
					"nack":             v.NACKCount,
					"pli":              v.PLICount,
					"jitter_ms":        int(v.Jitter * 1000),
					"bytes":            v.BytesReceived,
				}))
			}
		}

		if pair != nil {
			local, remote := candidates[pair.LocalCandidateID], candidates[pair.RemoteCandidateID]
			metrics.SessionCandidatePair.WithLabelValues(local.CandidateType.String(), remote.CandidateType.String(),
				remote.Protocol).Inc()
			metrics.SessionRTT.Observe(pair.CurrentRoundTripTime)
			fields = append(fields,
				wlog.String("local_candidate", fmt.Sprintf("%s %s:%d", local.CandidateType, local.IP, local.Port)),
				wlog.String("remote_candidate", fmt.Sprintf("%s %s:%d/%s", remote.CandidateType, remote.IP, remote.Port,
					remote.Protocol)),
				wlog.Int("rtt_ms", int(pair.CurrentRoundTripTime*1000)),
			)
		}

		var lost, late uint32

		s.mu.Lock()
		for _, t := range s.track {
			lost += t.lostPackets.Load()
			late += t.latePackets.Load()

			if kind, _, ok := strings.Cut(t.MimeType, "/"); ok && (kind == "audio" || kind == "video") {
				metrics.RTPPackets.WithLabelValues(kind, "unrecovered").Add(float64(t.lostPackets.Load()))
				metrics.RTPPackets.WithLabelValues(kind, "late").Add(float64(t.latePackets.Load()))
			}
		}
		s.mu.Unlock()

		s.observeSession()

		fields = append(fields, wlog.Int("unrecovered_lost", int(lost)), wlog.Int("late", int(late)))

		s.log.Info("session stats", fields...)
	})
}

func (s *RtcUploadMediaSession) observeSession() {
	kind := "record"
	if s.fileConfig.IsDiagnostic() {
		kind = "diagnostic"
	}

	metrics.SessionsTotal.WithLabelValues(strings.ToLower(s.pc.ICEConnectionState().String()), kind).Inc()
	metrics.SessionDuration.Observe(time.Since(s.createdAt).Seconds())

	if ms := s.iceConnectMs.Load(); ms > 0 {
		metrics.SessionICEConnect.Observe(float64(ms) / 1000)
	}
}

func observeInboundRTP(v webrtc.InboundRTPStreamStats) {
	metrics.RTPPackets.WithLabelValues(v.Kind, "received").Add(float64(v.PacketsReceived))

	if v.PacketsLost > 0 {
		metrics.RTPPackets.WithLabelValues(v.Kind, "lost").Add(float64(v.PacketsLost))
	}

	metrics.RTPFeedback.WithLabelValues(v.Kind, "nack").Add(float64(v.NACKCount))
	metrics.RTPFeedback.WithLabelValues(v.Kind, "pli").Add(float64(v.PLICount))
	metrics.RTPBytes.WithLabelValues(v.Kind).Add(float64(v.BytesReceived))
}

// candidateSummary counts candidates by type in an SDP, e.g. "host=2 srflx=1 relay=0":
// shows which paths the client was able to offer (no relay means TURN did not work on the client side).
func candidateSummary(sdp string) string {
	counts := map[string]int{"host": 0, "srflx": 0, "prflx": 0, "relay": 0}

	for _, line := range strings.Split(sdp, "\n") {
		if !strings.HasPrefix(line, "a=candidate:") {
			continue
		}

		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "typ" {
				counts[f[i+1]]++

				break
			}
		}
	}

	return fmt.Sprintf("host=%d srflx=%d prflx=%d relay=%d", counts["host"], counts["srflx"], counts["prflx"], counts["relay"])
}

func lossPercent(lost int32, received uint32) float64 {
	if lost <= 0 || received == 0 {
		return 0
	}

	return float64(int(float64(lost)/float64(int64(received)+int64(lost))*10000)) / 100
}

func (s *RtcUploadMediaSession) negotiate(sdpOffer string) error {
	s.offer = webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdpOffer,
	}

	// Set the remote SessionDescription
	err := s.pc.SetRemoteDescription(s.offer)
	if err != nil {
		return err
	}

	// Create answer
	var answer webrtc.SessionDescription

	answer, err = s.pc.CreateAnswer(nil)
	if err != nil {
		return err
	}

	gatherComplete := webrtc.GatheringCompletePromise(s.pc)

	err = s.pc.SetLocalDescription(answer)
	if err != nil {
		return err
	}

	<-gatherComplete

	s.answer = s.pc.LocalDescription()

	return nil
}

func (s *RtcUploadMediaSession) AnswerSDP() string {
	if s.answer != nil {
		return s.answer.SDP
	}

	return ""
}

func (s *RtcUploadMediaSession) ID() string {
	return s.id
}

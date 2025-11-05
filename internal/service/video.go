package service

import (
	"context"
	"fmt"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/h264writer"
	"github.com/pion/webrtc/v4/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
	"go.uber.org/atomic"
	"io"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/internal/model"
)

type Track struct {
	model.File
	countPkg int
	writer   io.WriteCloser `json:"-"`
	encoder  media.Writer   `json:"-"`
}

type RtcUploadVideoSession struct {
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
}

func NewWebRtcUploadSession(rec *WebRtcRecorder, pc *webrtc.PeerConnection, file *model.File) *RtcUploadVideoSession {
	id := model.NewID()
	session := &RtcUploadVideoSession{
		id:         id,
		fileConfig: file,
		rec:        rec,
		pc:         pc,
		log:        rec.log.With(wlog.String("session", id)),
		track:      make([]*Track, 0, 2),
	}

	session.ctx, session.cancel = context.WithCancel(context.Background())
	pc.OnTrack(session.onTrack)
	pc.OnICEConnectionStateChange(session.onICEConnectionStateChange)

	return session
}

func (s *RtcUploadVideoSession) onTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
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
	s.track = append(s.track, &t)
	s.fileConfig.Track = append(s.fileConfig.Track, t.Path)

	s.log.Debug(fmt.Sprintf("got %s: %s track, saving as %s", track.ID(), codec.MimeType, s.fileConfig.Name))
	if s.fileConfig.StartTime == 0 {
		s.fileConfig.StartTime = model.GetMillis()
	}

	s.countTrack.Add(1)

	defer func() {
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
	default:
		return // TODO
	}

	if t.encoder != nil {
		var (
			rtpPacket *rtp.Packet
			sample    *media.Sample
			lsn       uint16 = 0
		)

		builder := samplebuilder.New(45, pkt, codec.ClockRate,
			samplebuilder.WithRTPHeaders(true),
			samplebuilder.WithPacketReleaseHandler(func(pkt *rtp.Packet) {
				// if debugRtp {
				//s.log.Debug(fmt.Sprintf("rtp ts=%d seq=%d", pkt.Timestamp, pkt.SequenceNumber))
				//}
				if lsn != 0 && pkt.SequenceNumber != lsn+1 {
					s.log.Error(fmt.Sprintf("lost packets packet seq=%d, last=%d, count=%d", pkt.SequenceNumber,
						lsn, pkt.SequenceNumber-(lsn+1)))
				}

				lsn = pkt.SequenceNumber
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

func (s *RtcUploadVideoSession) onICEConnectionStateChange(connectionState webrtc.ICEConnectionState) {
	s.log.Debug(fmt.Sprintf("connection state has changed to %s", connectionState.String()))

	switch connectionState { //nolint:exhaustive
	case webrtc.ICEConnectionStateFailed:
		s.countTrack.Store(0) // TODO
		s.close()
	default:

	}
}

func (s *RtcUploadVideoSession) close() {
	s.log.Debug("close")
	if s.countTrack.Load() != 0 {
		s.log.Debug("wait close track")
		return
	}

	s.cancel()

	for _, track := range s.track {
		if track.encoder != nil {
			if closeErr := track.encoder.Close(); closeErr != nil {
				s.log.Error(fmt.Sprintf("closing encoder: %s", closeErr.Error()))
			}

			track.encoder = nil
		}

		if track.writer != nil {
			if closeErr := track.writer.Close(); closeErr != nil {
				s.log.Error(fmt.Sprintf("closing writer: %s", closeErr.Error()))
			}

			track.writer = nil
		}
	}

	// Gracefully shutdown the peer connection
	if closeErr := s.pc.Close(); closeErr != nil {
		s.log.Error(fmt.Sprintf("closing peer connection: %s", closeErr.Error()))
	}

	s.rec.stopVideoSession(s)
}

func (s *RtcUploadVideoSession) negotiate(sdpOffer string) error {
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

func (s *RtcUploadVideoSession) AnswerSDP() string {
	if s.answer != nil {
		return s.answer.SDP
	}

	return ""
}

func (s *RtcUploadVideoSession) ID() string {
	return s.id
}

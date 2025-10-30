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
	"io"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/internal/model"
)

type RtcUploadVideoSession struct {
	id       string
	answer   *webrtc.SessionDescription
	offer    webrtc.SessionDescription
	pc       *webrtc.PeerConnection
	log      *wlog.Logger
	file     *model.File
	cancel   context.CancelFunc
	ctx      context.Context
	rec      *WebRtcRecorder
	encoder  media.Writer
	countPkg int

	writer io.WriteCloser
}

func NewWebRtcUploadSession(rec *WebRtcRecorder, pc *webrtc.PeerConnection, file *model.File, w io.WriteCloser) *RtcUploadVideoSession {
	id := model.NewID()
	session := &RtcUploadVideoSession{
		id:     id,
		file:   file,
		rec:    rec,
		pc:     pc,
		writer: w,
		log:    rec.log.With(wlog.String("session", id)),
	}

	session.ctx, session.cancel = context.WithCancel(context.Background())
	pc.OnTrack(session.onTrack)
	pc.OnICEConnectionStateChange(session.onICEConnectionStateChange)

	return session
}

func (s *RtcUploadVideoSession) onTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	codec := track.Codec()

	var (
		err error
		pkt rtp.Depacketizer
	)

	s.log.Debug(fmt.Sprintf("got %s track, saving as %s", codec.MimeType, s.file.Name))
	s.file.StartTime = model.GetMillis()

	defer func() {
		s.log.Debug("closing writer")
		s.close()
	}()

	switch codec.MimeType {
	case webrtc.MimeTypeVP9:
		s.file.MimeType = codec.MimeType
		pkt = &codecs.VP9Packet{}

		s.encoder, err = ivfwriter.NewWith(s.writer, ivfwriter.WithCodec(codec.MimeType))
		if err != nil {
			s.log.Error(fmt.Sprintf("failed to open ivf file: %s", err))

			return
		}

	case webrtc.MimeTypeH264:
		s.file.MimeType = codec.MimeType
		pkt = &codecs.H264Packet{}
		s.encoder = h264writer.NewWith(s.writer)
	}

	if s.encoder != nil {
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
				if err = s.encoder.WriteRTP(pkt); err != nil {
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

				s.countPkg++

				if s.countPkg%1000 == 0 {
					s.log.Debug(fmt.Sprintf("receive rtc packet count %d", s.countPkg))
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
		s.close()
	default:

	}
}

func (s *RtcUploadVideoSession) close() {
	s.cancel()

	if s.encoder != nil {
		if closeErr := s.encoder.Close(); closeErr != nil {
			s.log.Error(fmt.Sprintf("closing encoder: %s", closeErr.Error()))
		}

		s.encoder = nil
	}

	if s.writer != nil {
		if closeErr := s.writer.Close(); closeErr != nil {
			s.log.Error(fmt.Sprintf("closing writer: %s", closeErr.Error()))
		}

		s.writer = nil
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

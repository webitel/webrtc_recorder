package service

import (
	"fmt"
	"strings"

	"github.com/pion/webrtc/v4"

	"github.com/webitel/wlog"

	webrtci "github.com/webitel/webrtc_recorder/infra/webrtc"
	"github.com/webitel/webrtc_recorder/internal/model"
)

type SessionStore interface {
	Get(id string) (model.RtcUploadVideoSession, error)
	Add(id string, sess model.RtcUploadVideoSession) error
	Remove(id string) bool
}

type WebRtcRecorder struct {
	log      *wlog.Logger
	api      webrtci.API
	sessions SessionStore

	transcoding *Transcoding
	temp        *TempFileService
}

func NewWebRtcRecorder(log *wlog.Logger, api webrtci.API, sess SessionStore, tmp *TempFileService, tr *Transcoding) *WebRtcRecorder {
	return &WebRtcRecorder{
		api:         api,
		log:         log.With(wlog.String("service", "webrtc")),
		sessions:    sess,
		temp:        tmp,
		transcoding: tr,
	}
}

func (svc *WebRtcRecorder) UploadP2PVideo(sdpOffer string, file model.File, ice []webrtci.ICEServer) (model.RtcUploadVideoSession, error) {
	var (
		peerConnection *webrtc.PeerConnection
		err            error
	)

	config := webrtc.Configuration{
		ICEServers: ice,
	}

	peerConnection, err = svc.api.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}

	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}

	if strings.Index(sdpOffer, "m=video") > -1 {
		if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}

	if strings.Index(sdpOffer, "m=audio") > -1 {
		if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
			return nil, err
		}
	}

	writeFile := &file

	session := NewWebRtcUploadSession(svc, peerConnection, writeFile)

	err = session.negotiate(sdpOffer)
	if err != nil {
		session.close()

		return nil, err
	}

	if err = svc.sessions.Add(session.id, session); err != nil {
		return nil, err
	}

	return session, nil
}

func (svc *WebRtcRecorder) RenegotiateP2P(id, sdpOffer string) (model.RtcUploadVideoSession, error) {
	session, err := svc.sessions.Get(id)
	if err != nil {
		return nil, fmt.Errorf("p2p session with id %s not found", id)
	}

	sess := session.(*RtcUploadMediaSession)

	// TODO singleflight
	err = sess.negotiate(sdpOffer)
	if err != nil {
		sess.close()

		return nil, err
	}

	return sess, nil
}

func (svc *WebRtcRecorder) CloseP2P(id string) error {
	session, err := svc.sessions.Get(id)
	if err != nil {
		return err
	}

	// TODO singleflight
	session.(*RtcUploadMediaSession).close()

	return nil
}

func (svc *WebRtcRecorder) stopVideoSession(s *RtcUploadMediaSession) {
	if !svc.sessions.Remove(s.id) {
		s.log.Debug("closing peer connection")

		return
	}

	if s.fileConfig.StartTime > 0 {
		s.fileConfig.EndTime = model.GetMillis()
	}

	err := svc.transcoding.CreateJob(s.fileConfig)
	if err != nil {
		s.log.Error(err.Error(), wlog.Err(err))

		err = svc.temp.DeleteFile(s.fileConfig)
		if err != nil {
			s.log.Error(err.Error(), wlog.Err(err))
		}
	}
}

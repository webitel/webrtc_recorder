package service

import (
	"fmt"
	"github.com/pion/webrtc/v4"
	webrtci "github.com/webitel/webrtc_recorder/infra/webrtc"
	"github.com/webitel/webrtc_recorder/internal/model"
	"github.com/webitel/wlog"
	"io"
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
	cache       *CacheService
}

func NewWebRtcRecorder(log *wlog.Logger, api webrtci.API, sess SessionStore, cache *CacheService, tr *Transcoding) *WebRtcRecorder {
	return &WebRtcRecorder{
		api:         api,
		log:         log.With(wlog.String("service", "webrtc")),
		sessions:    sess,
		cache:       cache,
		transcoding: tr,
	}
}

func (svc *WebRtcRecorder) UploadP2PVideo(sdpOffer string, file model.File, ice []webrtci.ICEServer) (model.RtcUploadVideoSession, error) {
	var peerConnection *webrtc.PeerConnection
	var err error
	var writer io.WriteCloser

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

	writeFile := &file
	writer, err = svc.cache.NewWriter(writeFile, "raw")
	if err != nil {
		return nil, err
	}

	session := NewWebRtcUploadSession(svc, peerConnection, writeFile, writer)

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

func (svc *WebRtcRecorder) RenegotiateP2P(id string, sdpOffer string) (model.RtcUploadVideoSession, error) {
	session, err := svc.sessions.Get(id)
	if err != nil {
		return nil, fmt.Errorf("p2p session with id %s not found", id)
	}
	sess := session.(*RtcUploadVideoSession)

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
	session.(*RtcUploadVideoSession).close()
	return nil
}

func (svc *WebRtcRecorder) stopVideoSession(s *RtcUploadVideoSession) {
	if !svc.sessions.Remove(s.id) {
		s.log.Debug("closing peer connection")
		return
	}

	err := svc.transcoding.CreateJob(s.file)
	if err != nil {
		s.log.Error(err.Error(), wlog.Err(err))
		err = svc.cache.DeleteFile(s.file)
		if err != nil {
			s.log.Error(err.Error(), wlog.Err(err))
		}
	}
}

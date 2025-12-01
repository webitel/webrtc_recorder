package handler

import (
	"context"
	"github.com/webitel/wlog"

	spb "github.com/webitel/webrtc_recorder/gen/storage"
	"github.com/webitel/webrtc_recorder/gen/webrtc_recorder"
	"github.com/webitel/webrtc_recorder/infra/grpc_srv"
	webrtci "github.com/webitel/webrtc_recorder/infra/webrtc"
	"github.com/webitel/webrtc_recorder/internal/model"
)

type WebRTCRecorderService interface {
	UploadP2PVideo(sdpOffer string, file model.File, ice []webrtci.ICEServer) (model.RtcUploadVideoSession, error)
	CloseP2P(id string) error
	RenegotiateP2P(id, sdpOffer string) (model.RtcUploadVideoSession, error)
}

type WebRTCRecorder struct {
	webrtc_recorder.UnimplementedWebRTCServiceServer

	log *wlog.Logger
	svc WebRTCRecorderService
}

func NewWebRTCRecorder(svc WebRTCRecorderService, s *grpc_srv.Server, l *wlog.Logger) *WebRTCRecorder {
	h := &WebRTCRecorder{
		svc: svc,
		log: l,
	}
	webrtc_recorder.RegisterWebRTCServiceServer(s, h)

	return h
}

func (w *WebRTCRecorder) UploadP2PVideo(ctx context.Context, in *webrtc_recorder.UploadP2PVideoRequest) (*webrtc_recorder.UploadP2PVideoResponse, error) {
	authUser, err := grpc_srv.SessionFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	i := make([]webrtci.ICEServer, 0, len(in.GetIceServers()))
	for _, server := range in.GetIceServers() {
		i = append(i, webrtci.ICEServer{
			URLs:           server.GetUrls(),
			Username:       server.GetUsername(),
			Credential:     server.GetCredential(),
			CredentialType: 0, // TODO
		})
	}

	name := in.GetName()
	if name == "" {
		name = model.NewID()
	}

	file := model.File{
		Name:       name,
		UUID:       in.GetUuid(),
		DomainID:   int(authUser.DomainID),
		UploadedBy: int(authUser.UserID),
		CreatedAt:  model.GetMillis(),
		Channel:    getChannel(in.GetChannel()),
	}

	sess, err := w.svc.UploadP2PVideo(in.GetSdpOffer(), file, i)
	if err != nil {
		return nil, err
	}

	return &webrtc_recorder.UploadP2PVideoResponse{
		SdpAnswer: sess.AnswerSDP(),
		Id:        sess.ID(),
	}, nil
}

func (w *WebRTCRecorder) StopP2PVideo(ctx context.Context, in *webrtc_recorder.StopP2PVideoRequest) (*webrtc_recorder.StopP2PVideoResponse, error) {
	e := w.svc.CloseP2P(in.GetId())
	if e != nil {
		return nil, e
	}

	return &webrtc_recorder.StopP2PVideoResponse{}, nil
}

func (w *WebRTCRecorder) RenegotiateP2PVideo(ctx context.Context, in *webrtc_recorder.RenegotiateP2PVideoRequest) (*webrtc_recorder.RenegotiateP2PVideoResponse, error) {
	s, err := w.svc.RenegotiateP2P(in.GetId(), in.GetSdpOffer())
	if err != nil {
		return nil, err
	}

	return &webrtc_recorder.RenegotiateP2PVideoResponse{
		SdpAnswer: s.AnswerSDP(),
	}, nil
}

func getChannel(ch spb.UploadFileChannel) int {
	switch ch { // TODO allow other
	case spb.UploadFileChannel_CallChannel:
		return int(spb.UploadFileChannel_CallChannel)
	default:
		return int(spb.UploadFileChannel_ScreenSharingChannel)
	}
}

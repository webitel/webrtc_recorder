//go:build wireinject
// +build wireinject

package cmd

import (
	"context"

	"github.com/google/wire"

	"github.com/webitel/webrtc_recorder/config"
	"github.com/webitel/webrtc_recorder/internal/handler"
	"github.com/webitel/webrtc_recorder/internal/service"
	"github.com/webitel/webrtc_recorder/internal/store"
)

var wireAppResourceSet = wire.NewSet(
	log, grpcSrv, setupCluster, setupSql, webrtcApi, authManager, storageClient,
)

var wireAppHandlersSet = wire.NewSet(
	store.NewSessionStore,
	store.NewFileJobStore,

	service.NewTempFileService,
	service.NewUploader,
	service.NewTranscoding, wire.Bind(new(service.FileJobStore), new(*store.FileJobStore)),

	service.NewWebRtcRecorder, wire.Bind(new(service.SessionStore), new(*store.SessionStore)),

	handler.NewWebRTCRecorder, wire.Bind(new(handler.WebRTCRecorderService), new(*service.WebRtcRecorder)),
)

func initAppResources(context.Context, *config.Config) (*resources, func(), error) {
	wire.Build(wireAppResourceSet, wire.Struct(new(resources),
		"log", "store", "grpcSrv", "cluster", "webrtc", "auth", "storage", "cfg"))

	return &resources{}, nil, nil
}

func initAppHandlers(context.Context, *resources) (*handlers, error) {
	wire.Build(wireAppHandlersSet,
		wire.FieldsOf(new(*resources), "log", "grpcSrv", "webrtc", "storage", "cfg", "store"),
		wire.Struct(new(handlers), "webrtcRecorder"),
	)

	return &handlers{}, nil
}

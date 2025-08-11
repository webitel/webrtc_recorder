package storage

import (
	"sync"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/gen/storage"
	"github.com/webitel/webrtc_recorder/infra/grpc_client"
)

type Storage struct {
	cli        *grpc_client.Client[storage.FileServiceClient]
	startOnce  sync.Once
	consulAddr string
	log        *wlog.Logger
}

var storageServiceName = "storage"

func New(consulAddr string, log *wlog.Logger) *Storage {
	return &Storage{
		consulAddr: consulAddr,
		log:        log.With(wlog.Namespace("context")).With(wlog.String("scope", storageServiceName)),
	}
}

func (s *Storage) Start() error {
	s.log.Debug("starting")
	var err error

	s.startOnce.Do(func() {
		s.cli, err = grpc_client.NewClient(s.consulAddr, storageServiceName, storage.NewFileServiceClient)
		if err != nil {
			return
		}
	})
	return err
}

func (s *Storage) Stop() {
	s.log.Debug("stopping")
	_ = s.cli.Close()
}

func (s *Storage) Api() storage.FileServiceClient {
	return s.cli.Api
}

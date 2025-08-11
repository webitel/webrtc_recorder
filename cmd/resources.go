package cmd

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/config"
	"github.com/webitel/webrtc_recorder/infra/auth"
	"github.com/webitel/webrtc_recorder/infra/consul"
	"github.com/webitel/webrtc_recorder/infra/grpc_srv"
	_ "github.com/webitel/webrtc_recorder/infra/resolver"
	"github.com/webitel/webrtc_recorder/infra/sql"
	"github.com/webitel/webrtc_recorder/infra/sql/pgsql"
	"github.com/webitel/webrtc_recorder/infra/storage"
	"github.com/webitel/webrtc_recorder/infra/webrtc"
	"github.com/webitel/webrtc_recorder/internal/handler"
	"github.com/webitel/webrtc_recorder/internal/model"
)

type handlers struct {
	webrtcRecorder *handler.WebRTCRecorder
}

type resources struct {
	log     *wlog.Logger
	grpcSrv *grpc_srv.Server
	cluster *consul.Cluster
	store   sql.Store
	webrtc  webrtc.API
	auth    auth.Manager
	cfg     *config.Config
	storage *storage.Storage
}

func grpcSrv(cfg *config.Config, l *wlog.Logger, am auth.Manager) (*grpc_srv.Server, func(), error) {
	s, err := grpc_srv.New(cfg.Service.Address, l, am)
	if err != nil {
		return nil, nil, err
	}

	return s, func() {
		if err := s.Shutdown(); err != nil {
			l.Error(err.Error(), wlog.Err(err))
		}
	}, nil
}

func log(cfg *config.Config) (*wlog.Logger, func(), error) {
	logSettings := cfg.Log

	if !logSettings.Console && !logSettings.Otel && len(logSettings.File) == 0 {
		logSettings.Console = true
	}

	logConfig := &wlog.LoggerConfiguration{
		EnableConsole: logSettings.Console,
		ConsoleJson:   false,
		ConsoleLevel:  logSettings.Lvl,
	}

	if logSettings.File != "" {
		logConfig.FileLocation = logSettings.File
		logConfig.EnableFile = true
		logConfig.FileJson = true
		logConfig.FileLevel = logSettings.Lvl
	}

	l := wlog.NewLogger(logConfig)
	wlog.RedirectStdLog(l)
	wlog.InitGlobalLogger(l)

	exit := func() {
	}

	return l, exit, nil
}

func setupCluster(cfg *config.Config, srv *grpc_srv.Server, l *wlog.Logger) (*consul.Cluster, func(), error) {
	c := consul.NewCluster(model.ServiceName, cfg.Service.Consul, l)
	host := srv.Host()

	err := c.Start(cfg.Service.ID, host, srv.Port())
	if err != nil {
		return nil, nil, err
	}

	return c, func() {
		c.Stop()
	}, nil
}

func setupSQL(ctx context.Context, log *wlog.Logger, cfg *config.Config) (sql.Store, func(), error) {
	s, err := pgsql.New(ctx, cfg.SQLSettings.DSN, log)
	if err != nil {
		return nil, nil, err
	}

	return s, func() {
		err = s.Close()
		if err != nil {
			wlog.Error(err.Error(), wlog.Err(err))
		}
	}, nil
}

func webrtcAPI(log *wlog.Logger, cfg *config.Config) (webrtc.API, func(), error) {
	if len(cfg.Rtc.Codecs.Value()) == 0 {
		return nil, nil, errors.New("webrtc codecs is empty")
	}

	var (
		udpRange *webrtc.PortRange
		err      error
	)

	if len(cfg.Rtc.EphemeralUDPPortRange) != 0 {
		udpRange, err = portRangeFromString(cfg.Rtc.EphemeralUDPPortRange)
		if err != nil {
			return nil, nil, err
		}
	}

	return webrtc.NewAPI(log, &webrtc.Settings{
		Codecs: cfg.Rtc.Codecs.Value(),
		ICE: &webrtc.ICESettings{
			DisconnectedTimeout: cfg.Rtc.Ice.DisconnectedTimeout,
			FailedTimeout:       cfg.Rtc.Ice.FailedTimeout,
			KeepAliveInterval:   cfg.Rtc.Ice.KeepAliveInterval,
		},
		EphemeralUDPPortRange: udpRange,
	}), func() {}, nil
}

func authManager(cfg *config.Config, log *wlog.Logger) (auth.Manager, func(), error) {
	manager := auth.NewAuthManager(1000, 60, cfg.Service.Consul, log)

	err := manager.Start()
	if err != nil {
		return nil, nil, err
	}

	return manager, func() {
		manager.Stop()
	}, nil
}

func storageClient(cfg *config.Config, log *wlog.Logger) (*storage.Storage, func(), error) {
	fileStore := storage.New(cfg.Service.Consul, log)

	err := fileStore.Start()
	if err != nil {
		return nil, nil, err
	}

	return fileStore, func() {
		fileStore.Stop()
	}, nil
}

func validUint16(i int) bool {
	return i >= 0 && i <= math.MaxInt16
}

func portRangeFromString(str string) (*webrtc.PortRange, error) {
	l := strings.Split(str, "-")
	if len(l) != 2 {
		return nil, errors.New("invalid port range format")
	}

	udpRange := &webrtc.PortRange{}

	err := setPortRange(&udpRange.Min, l[0])
	if err != nil {
		return nil, err
	}

	err = setPortRange(&udpRange.Max, l[1])
	if err != nil {
		return nil, err
	}

	return udpRange, nil
}

func setPortRange(dst *uint16, src string) error {
	p, err := strconv.Atoi(src)
	if err != nil {
		return err
	}

	if !validUint16(p) {
		return errors.New("invalid EphemeralUDPPortRange format")
	}

	*dst = uint16(p)

	return nil
}

package cmd

import (
	"context"
	"fmt"
	"github.com/urfave/cli/v2"
	"github.com/webitel/webrtc_recorder/config"
	"github.com/webitel/wlog"
	"golang.org/x/sync/errgroup"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type App struct {
	log *wlog.Logger
	cfg *config.Config
	ctx context.Context
	eg  errgroup.Group
}

func NewApp(cfg *config.Config, ctx context.Context) *App {
	return &App{
		cfg: cfg,
		log: wlog.GlobalLogger(),
		ctx: ctx,
	}
}

func (a *App) Run() (func(), error) {

	r, shutdown, err := initAppResources(a.ctx, a.cfg)
	if err != nil {
		return nil, err
	}

	a.log = r.log

	_, err = initAppHandlers(a.ctx, r)
	if err != nil {
		return shutdown, err
	}

	a.eg.Go(func() error {
		a.log.Info(fmt.Sprintf("listen grpc %s:%d", r.grpcSrv.Host(), r.grpcSrv.Port()))
		return r.grpcSrv.Listen()
	})

	return shutdown, nil
}

func apiCmd(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:    "server",
		Aliases: []string{"a"},
		Usage:   "Start webrtc_recorder server",
		Flags:   apiFlags(cfg),
		Action: func(c *cli.Context) error {
			interruptChan := make(chan os.Signal, 1)

			ctx, cancel := context.WithCancel(c.Context)

			app := NewApp(cfg, ctx)
			shutdown, err := app.Run()
			defer func() {
				cancel()
				if shutdown != nil {
					shutdown()
				}
			}()
			if err != nil {
				wlog.Error(err.Error(), wlog.Err(err))
				return err
			}
			signal.Notify(interruptChan, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
			<-interruptChan
			return nil
		},
	}
}

func apiFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "service-id",
			Category:    "server",
			Usage:       "service id ",
			Value:       "1",
			Destination: &cfg.Service.Id,
			Aliases:     []string{"i"},
			EnvVars:     []string{"ID"},
		},
		&cli.StringFlag{
			Name:        "bind-address",
			Category:    "server",
			Usage:       "address that should be bound to for internal cluster communications",
			Value:       "localhost:50011",
			Destination: &cfg.Service.Address,
			Aliases:     []string{"b"},
			EnvVars:     []string{"BIND_ADDRESS"},
		},
		&cli.StringFlag{
			Name:        "consul-discovery",
			Category:    "server",
			Usage:       "service discovery address",
			Value:       "127.0.0.1:8500",
			Destination: &cfg.Service.Consul,
			Aliases:     []string{"c"},
			EnvVars:     []string{"CONSUL"},
		},
		&cli.StringFlag{
			Name:        "postgresql-dsn",
			Category:    "database",
			Usage:       "Postgres connection string",
			EnvVars:     []string{"DATA_SOURCE"},
			Value:       "postgres://postgres:postgres@localhost:5432/webitel?sslmode=disable",
			Destination: &cfg.SqlSettings.DSN,
		},

		&cli.StringSliceFlag{
			Name:        "webrtc-codecs",
			Category:    "webrtc",
			Usage:       "webrtc support codecs (video/VP9, video/H264)",
			Value:       cli.NewStringSlice("video/VP9", "video/H264"),
			EnvVars:     []string{"WEBRTC_CODECS"},
			Destination: &cfg.Rtc.Codecs,
		},
		//&cli.StringSliceFlag{
		//	Name:        "webrtc-network",
		//	Category:    "webrtc",
		//	Usage:       "webrtc support network (tcp, tcp)",
		//	Value:       cli.NewStringSlice("tcp", "udp"),
		//	EnvVars:     []string{"WEBRTC_NETWORK"},
		//	Destination: &cfg.Rtc.Network,
		//},
		&cli.DurationFlag{
			Name:        "webrtc-ice-disconnect-timeout",
			Category:    "webrtc",
			Usage:       "ICE disconnect timeout",
			Value:       time.Second * 5,
			EnvVars:     []string{"WEBRTC_ICE_DISCONNECT_TIMEOUT"},
			Destination: &cfg.Rtc.Ice.DisconnectedTimeout,
		},
		&cli.DurationFlag{
			Name:        "webrtc-ice-failed-timeout",
			Category:    "webrtc",
			Usage:       "ICE failed timeout",
			Value:       time.Second * 15,
			EnvVars:     []string{"WEBRTC_ICE_FAILED_TIMEOUT"},
			Destination: &cfg.Rtc.Ice.FailedTimeout,
		},
		&cli.DurationFlag{
			Name:        "webrtc-ice-keepalive-timeout",
			Category:    "webrtc",
			Usage:       "ICE keep alive timeout",
			Value:       time.Second * 5,
			EnvVars:     []string{"WEBRTC_ICE_KEEPALIVE_TIMEOUT"},
			Destination: &cfg.Rtc.Ice.KeepAliveInterval,
		},
		&cli.StringFlag{
			Name:        "webrtc-udp-port-range",
			Category:    "webrtc",
			Usage:       "UDP port range",
			Value:       "10000-20000",
			EnvVars:     []string{"WEBRTC_UDP_PORT_RANGE"},
			Destination: &cfg.Rtc.EphemeralUDPPortRange,
		},
		&cli.StringFlag{
			Name:        "cache-dir",
			Category:    "cache",
			Usage:       "Temp file cache dir",
			Value:       "./temp",
			EnvVars:     []string{"CACHE_TEMP_DIR"},
			Destination: &cfg.TempDir,
		},
		&cli.IntFlag{
			Name:        "uploader-workers",
			Category:    "uploader",
			Usage:       "uploader workers",
			Value:       10,
			EnvVars:     []string{"UPLOADER_WORKERS"},
			Destination: &cfg.Uploader.Workers,
		},
		&cli.IntFlag{
			Name:        "uploader-queue",
			Category:    "uploader",
			Usage:       "uploader queue size",
			Value:       2,
			EnvVars:     []string{"UPLOADER_QUEUE"},
			Destination: &cfg.Uploader.Queue,
		},
		&cli.IntFlag{
			Name:        "uploader-max-retry",
			Category:    "uploader",
			Usage:       "uploader retry count",
			Value:       20,
			EnvVars:     []string{"UPLOADER_MAX_RETRY"},
			Destination: &cfg.Uploader.MaxRetry,
		},

		&cli.IntFlag{
			Name:        "transcoding-workers",
			Category:    "transcoding",
			Usage:       "transcoding workers",
			Value:       4,
			EnvVars:     []string{"TRANSCODING_WORKERS"},
			Destination: &cfg.Transcoding.Workers,
		},
		&cli.IntFlag{
			Name:        "transcoding-queue",
			Category:    "transcoding",
			Usage:       "transcoding queue size",
			Value:       1,
			EnvVars:     []string{"TRANSCODING_QUEUE"},
			Destination: &cfg.Transcoding.Queue,
		},
		&cli.IntFlag{
			Name:        "transcoding-max-retry",
			Category:    "transcoding",
			Usage:       "transcoding retry count",
			Value:       10,
			EnvVars:     []string{"TRANSCODING_MAX_RETRY"},
			Destination: &cfg.Transcoding.MaxRetry,
		},
	}
}

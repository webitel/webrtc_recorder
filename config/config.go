package config

import (
	"time"

	"github.com/urfave/cli/v2"
)

type Config struct {
	TempDir     string
	Service     Service
	Log         LogSettings
	SQLSettings SQLSettings
	Rtc         RtcSettings
	Uploader    UploaderSettings
	Transcoding TranscodingSettings
}

type TranscodingSettings struct {
	Workers  int
	Queue    int
	MaxRetry int
}

type UploaderSettings struct {
	Workers  int
	Queue    int
	MaxRetry int
}

type SQLSettings struct {
	DSN string
}

type Service struct {
	ID      string
	Address string
	Consul  string
}

type RtcSettings struct {
	Codecs cli.StringSlice
	// Network cli.StringSlice
	Ice struct {
		DisconnectedTimeout time.Duration
		FailedTimeout       time.Duration
		KeepAliveInterval   time.Duration
	}
	EphemeralUDPPortRange string // 10000-20000
}

type LogSettings struct {
	Lvl     string
	JSON    bool
	Otel    bool
	File    string
	Console bool
}

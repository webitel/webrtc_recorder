package auth

import (
	"context"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/gen/api"
	"github.com/webitel/webrtc_recorder/infra/grpc_client"
)

var authServiceName = "go.webitel.app"

type Manager interface {
	Start() error
	Stop()
	GetSession(ctx context.Context, token string) (*Session, error)
	ProductLimit(ctx context.Context, token, productName string) (int, error)
}

type authManager struct {
	session    *expirable.LRU[string, *Session]
	startOnce  sync.Once
	consulAddr string
	auth       *grpc_client.Client[api.AuthClient]
	customer   *grpc_client.Client[api.CustomersClient]

	log *wlog.Logger
}

func NewAuthManager(cacheSize int, cacheTime int64, consulAddr string, log *wlog.Logger) Manager {
	if cacheTime < 1 {
		// 0 disabled cache
		cacheTime = 1
	}
	return &authManager{
		consulAddr: consulAddr,
		session:    expirable.NewLRU[string, *Session](cacheSize, nil, time.Second*time.Duration(cacheTime)),
		log:        log.With(wlog.Namespace("context")).With(wlog.String("scope", "auth_manager")),
	}
}

func (am *authManager) Start() error {
	am.log.Debug("starting")
	var err error

	am.startOnce.Do(func() {
		am.auth, err = grpc_client.NewClient(am.consulAddr, authServiceName, api.NewAuthClient)
		if err != nil {
			return
		}

		am.customer, err = grpc_client.NewClient(am.consulAddr, authServiceName, api.NewCustomersClient)
		if err != nil {
			return
		}
	})
	return err
}

func (am *authManager) Stop() {
	am.log.Debug("stopping")
	_ = am.auth.Close()
	_ = am.customer.Close()
}

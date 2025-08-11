package grpc_srv

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/webitel/wlog"

	"github.com/webitel/webrtc_recorder/infra/auth"
	"github.com/webitel/webrtc_recorder/infra/grpc_client"
)

const (
	RequestContextName    = "grpc_ctx"
	RequestContextSession = "session"
)

var ErrUnauthenticated = status.Error(codes.Unauthenticated, "Unauthenticated")

type Server struct {
	*grpc.Server

	Addr     string
	host     string
	port     int
	log      *wlog.Logger
	listener net.Listener
	auth     auth.Manager
}

// New provides a new gRPC server.
func New(addr string, log *wlog.Logger, am auth.Manager) (*Server, error) {
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(),
		grpc.UnaryInterceptor(unaryInterceptor(am, log)),
	)

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	h, p, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		return nil, err
	}

	port, _ := strconv.Atoi(p)

	if h == "::" {
		h = publicAddr()
	}

	return &Server{
		Addr:     addr,
		Server:   s,
		log:      log,
		host:     h,
		port:     port,
		listener: l,
	}, nil
}

func (s *Server) Listen() error {
	return s.Serve(s.listener)
}

func (s *Server) Shutdown() error {
	s.log.Debug("receive shutdown grpc")
	err := s.listener.Close()
	s.GracefulStop()

	return err
}

func (s *Server) Host() string {
	if e, ok := os.LookupEnv("PROXY_GRPC_HOST"); ok {
		return e
	}

	return s.host
}

func (s *Server) Port() int {
	return s.port
}

func publicAddr() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}

			if isPublicIP(ip) {
				return ip.String()
			}
			// process IP address
		}
	}

	return ""
}

func isPublicIP(IP net.IP) bool {
	if IP.IsLoopback() || IP.IsLinkLocalMulticast() || IP.IsLinkLocalUnicast() {
		return false
	}

	return true
}

func unaryInterceptor(am auth.Manager, log *wlog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()

		_, session, err := getSessionFromCtx(am, ctx)
		if err != nil {
			return nil, err
		}

		ctx = context.WithValue(ctx, RequestContextSession, session)

		h, err := handler(ctx, req)

		l := log.With(wlog.String("method", info.FullMethod))

		if err != nil {
			l.Error(err.Error(), wlog.Float64("duration_ms", float64(time.Since(start).Microseconds())/float64(1000)))
		} else {
			l.Debug(fmt.Sprintf("[OK] %s", info.FullMethod), wlog.Float64("duration_ms", float64(time.Since(start).Microseconds())/float64(1000)))
		}

		return h, err
	}
}

func getSessionFromCtx(am auth.Manager, ctx context.Context) (metadata.MD, *auth.Session, error) {
	var (
		session *auth.Session
		err     error
		token   []string
		info    metadata.MD
		ok      bool
	)

	v := ctx.Value(RequestContextName)
	info, ok = v.(metadata.MD)

	// todo
	if !ok {
		info, ok = metadata.FromIncomingContext(ctx)
	}

	if !ok {
		return info, nil, ErrUnauthenticated
	} else {
		token = info.Get(grpc_client.TokenHeaderName)
	}

	if len(token) < 1 {
		return info, nil, ErrUnauthenticated
	}

	session, err = am.GetSession(ctx, token[0])
	if err != nil {
		return info, nil, err
	}

	if session.IsExpired() {
		return info, nil, ErrUnauthenticated
	}

	return info, session, nil
}

func SessionFromCtx(ctx context.Context) (*auth.Session, error) {
	sess := ctx.Value(RequestContextSession)
	if sess == nil {
		return nil, ErrUnauthenticated
	}

	return sess.(*auth.Session), nil
}

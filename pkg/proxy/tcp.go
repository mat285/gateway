package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/mat285/gateway/pkg/log"
)

type TCPProxyConfig struct {
	ListenPort string   `yaml:"listenPort"`
	Upstreams  []string `yaml:"upstreams"`
}

type TCPProxy struct {
	Config TCPProxyConfig

	Listener net.Listener

	upstream     int
	upstreamLock *sync.Mutex

	cancel context.CancelFunc
	done   chan struct{}
}

func NewTCPProxy(config TCPProxyConfig) *TCPProxy {
	return &TCPProxy{
		Config:       config,
		upstream:     0,
		upstreamLock: &sync.Mutex{},
	}
}

func (p *TCPProxy) Start(ctx context.Context) error {
	logger := log.GetLogger(ctx)
	logger.Infof("Starting TCP proxy %s %v", p.Config.ListenPort, p.Config.Upstreams)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%s", p.Config.ListenPort))
	if err != nil {
		return err
	}
	defer listener.Close()
	p.Listener = listener
	ctx, p.cancel = context.WithCancel(ctx)
	defer p.cancel()
	p.done = make(chan struct{})
	defer close(p.done)
	logger.Infof("TCP proxy started %s %v", p.Config.ListenPort, p.Config.Upstreams)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		logger.Infof("Accepted connection %s", conn.RemoteAddr())
		go p.handleConnection(ctx, conn)
	}
}

func (p *TCPProxy) Stop() {
	p.cancel()
	p.Listener.Close()
	if p.done != nil {
		<-p.done
	}
}

func (p *TCPProxy) handleConnection(ctx context.Context, conn net.Conn) {
	logger := log.GetLogger(ctx)
	defer func() {
		err := recover()
		if err != nil {
			logger.Infof("Error handling connection %v", err)
		}
	}()
	upstream := p.getUpstream()
	logger.Infof("Proxying to %s", upstream)
	upstreamConn, err := net.Dial("tcp", upstream)
	if err != nil {
		logger.Infof("Error dialing upstream %s %v", upstream, err)
		return
	}
	defer upstreamConn.Close()

	done := make(chan struct{})
	defer close(done)
	go func() {
		defer func() {
			done <- struct{}{}
		}()
		io.Copy(conn, upstreamConn)
	}()
	go func() {
		defer func() {
			done <- struct{}{}
		}()
		io.Copy(upstreamConn, conn)
	}()
	select {
	case <-ctx.Done():
		return
	case <-done:
		<-done
		return
	}
}

func (p *TCPProxy) getUpstream() string {
	p.upstreamLock.Lock()
	defer p.upstreamLock.Unlock()
	upstream := p.upstream
	p.upstream = (p.upstream + 1) % len(p.Config.Upstreams)
	return p.Config.Upstreams[upstream]
}

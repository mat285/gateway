package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/mat285/gateway/pkg/log"
	"github.com/mat285/gateway/pkg/wait"
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
	defer func() {
		conn.Close()
	}()
	upstreamConn, err := p.getUpstreamConn(ctx)
	if err != nil {
		logger.Infof("Error getting upstream connection %v", err)
		return
	}
	defer upstreamConn.Close()

	closeWrite := make(chan struct{})
	closeRead := make(chan struct{})
	wg := wait.NewGroup()
	wg.Add(2)
	go func() {
		defer func() {
			wg.Done()
			closeRead <- struct{}{}
			close(closeRead)
		}()
		io.Copy(conn, upstreamConn)
	}()
	go func() {
		defer func() {
			wg.Done()
			closeWrite <- struct{}{}
			close(closeWrite)
		}()
		io.Copy(upstreamConn, conn)
	}()
	select {
	case <-ctx.Done():
		conn.Close()
		upstreamConn.Close()
		wg.WaitTimeout(10 * time.Second)
	case <-closeWrite:
	case <-closeRead:
		conn.Close()
		upstreamConn.Close()
		wg.Wait()
		return
	}
}

func (p *TCPProxy) getUpstreamConn(ctx context.Context) (net.Conn, error) {
	logger := log.GetLogger(ctx)
	upstreams := p.getUpstreams()
	for _, upstream := range upstreams {
		logger.Infof("Dialing upstream %s", upstream)
		upstreamConn, err := net.Dial("tcp", upstream)
		if err != nil {
			logger.Errorf("Error dialing upstream %s %v", upstream, err)
			continue
		}
		logger.Infof("Connected to upstream %s", upstream)
		return upstreamConn, nil
	}
	return nil, fmt.Errorf("no upstreams available")
}

func (p *TCPProxy) getUpstreams() []string {
	p.upstreamLock.Lock()
	defer p.upstreamLock.Unlock()
	upstream := p.upstream
	p.upstream = (p.upstream + 1) % len(p.Config.Upstreams)

	upstreams := make([]string, 0, len(p.Config.Upstreams))
	for i := 0; i < len(p.Config.Upstreams); i++ {
		index := (upstream + i) % len(p.Config.Upstreams)
		upstreams = append(upstreams, p.Config.Upstreams[index])
	}
	return upstreams
}

func (p *TCPProxy) getUpstream() string {
	p.upstreamLock.Lock()
	defer p.upstreamLock.Unlock()
	upstream := p.upstream
	p.upstream = (p.upstream + 1) % len(p.Config.Upstreams)
	return p.Config.Upstreams[upstream]
}

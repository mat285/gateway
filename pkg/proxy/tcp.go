package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
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
	fmt.Println("Starting TCP proxy", p.Config.ListenPort, p.Config.Upstreams)
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
	fmt.Println("TCP proxy started", p.Config.ListenPort, p.Config.Upstreams)
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
		fmt.Println("Accepted connection", conn.RemoteAddr())
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
	defer func() {
		err := recover()
		if err != nil {
			fmt.Println("Error handling connection", err)
		}
	}()
	upstream := p.getUpstream()
	fmt.Println("Proxying to", upstream)
	upstreamConn, err := net.Dial("tcp", upstream)
	if err != nil {
		fmt.Println("Error dialing upstream", upstream, err)
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

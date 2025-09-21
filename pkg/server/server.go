package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mat285/gateway/pkg/kubectl"
	"github.com/mat285/gateway/pkg/proxy"
)

type Config struct {
}

type ProxySpec struct {
	GatewayPort string `yaml:"gatewayPort"`
	NodePort    string `yaml:"nodePort"`
}

type Server struct {
	Lock *sync.Mutex

	Config Config

	TCPProxies map[string]*proxy.TCPProxy
	newProxy   chan ProxySpec

	Nodes map[string]bool

	cancel context.CancelFunc
	done   chan struct{}
}

func NewServer(config Config) *Server {
	return &Server{
		Lock:       new(sync.Mutex),
		Config:     config,
		TCPProxies: make(map[string]*proxy.TCPProxy),
		newProxy:   make(chan ProxySpec, 1024),
		Nodes:      make(map[string]bool),
	}
}

func (s *Server) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)
	defer s.cancel()
	s.done = make(chan struct{})
	defer close(s.done)

	var wg sync.WaitGroup
	run := func(ctx context.Context, f func(ctx context.Context)) {
		wg.Add(1)
		go func() {
			defer s.cancel()
			defer wg.Done()
			f(ctx)
		}()
	}

	run(ctx, s.handleProxies)
	run(ctx, s.watchNodes)
	run(ctx, s.watchServices)

	wg.Wait()
	return nil
}

func (s *Server) watchServices(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
		}
		fmt.Println("fetching services")
		namespaces, err := kubectl.GetNamespaces(ctx)
		if err != nil {
			fmt.Println("Error getting namespaces", err)
			continue
		}
		fmt.Println("namespaces", namespaces)
		services, err := kubectl.GetServices(ctx, namespaces...)
		if err != nil {
			fmt.Println("Error getting services", err)
			continue
		}
		portMap := make(map[string]string)
		for _, service := range services {
			if service.Spec.Type != "NodePort" {
				continue
			}
			gatewayMap := make(map[string]string)
			for annotation, value := range service.Metadata.Annotations {
				if annotation == "gateway.nori.ninja/proxy-port" {
					parts := strings.Split(value, ":")
					if len(parts) != 2 {
						continue
					}
					fmt.Println("gateway port", parts[0], parts[1])
					gatewayMap[parts[1]] = parts[0]
				}
			}
			for _, port := range service.Spec.Ports {
				if port.NodePort.StringVal == "" {
					continue
				}
				if _, ok := gatewayMap[port.NodePort.StringVal]; !ok {
					fmt.Println("no gateway port found for", port.NodePort.StringVal)
					continue
				}
				portMap[port.NodePort.StringVal] = gatewayMap[port.NodePort.StringVal]
			}
		}
		s.Lock.Lock()
		for nodePort, portName := range portMap {
			existing, ok := s.TCPProxies[portName]
			if ok && (len(existing.Config.Upstreams) == 0 || !strings.HasSuffix(existing.Config.Upstreams[0], nodePort)) {
				fmt.Println("stopping proxy", portName)
				existing.Stop()
				delete(s.TCPProxies, portName)
			}
			fmt.Println("new proxy", portName, nodePort)
			s.newProxy <- ProxySpec{
				GatewayPort: portName,
				NodePort:    nodePort,
			}

		}
		s.Lock.Unlock()
	}
}

func (s *Server) handleProxies(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case proxySpec := <-s.newProxy:
			fmt.Println("New proxy", proxySpec)
			s.Lock.Lock()
			if _, ok := s.TCPProxies[proxySpec.GatewayPort]; ok {
				s.Lock.Unlock()
				fmt.Println("Proxy already exists", proxySpec.GatewayPort)
				continue
			}
			upstreams := make([]string, 0, len(s.Nodes))
			for node := range s.Nodes {
				upstreams = append(upstreams, fmt.Sprintf("%s:%s", node, proxySpec.NodePort))
			}
			tcpProxy := proxy.NewTCPProxy(proxy.TCPProxyConfig{
				ListenPort: proxySpec.GatewayPort,
				Upstreams:  upstreams,
			})
			s.TCPProxies[proxySpec.GatewayPort] = tcpProxy
			s.Lock.Unlock()
			go s.watchProxy(ctx, proxySpec, tcpProxy)
		}
	}
}

func (s *Server) watchProxy(ctx context.Context, spec ProxySpec, tcpProxy *proxy.TCPProxy) {
	defer func() {
		fmt.Println("Stopping TCP proxy", tcpProxy.Config.ListenPort)
		s.Lock.Lock()
		delete(s.TCPProxies, tcpProxy.Config.ListenPort)
		s.Lock.Unlock()

		s.cancel()

	}()
	fmt.Println("Starting TCP proxy")
	err := tcpProxy.Start(ctx)
	if err != nil {
		fmt.Println("Error starting TCP proxy", err)
	}
}

func (s *Server) watchNodes(ctx context.Context) {
	for {
		s.fetchNodes(ctx)

		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
			continue
		}

	}
}

func (s *Server) fetchNodes(ctx context.Context) {
	fmt.Println("fetching nodes")
	currentNodes, err := kubectl.GetNodes(ctx)
	if err != nil {
		fmt.Println("Error getting nodes", err)
		return
	}
	replace := make(map[string]bool)
	for _, node := range currentNodes {
		replace[node] = true
	}

	fmt.Println("replacing nodes", replace)
	s.Lock.Lock()
	s.Nodes = replace
	s.Lock.Unlock()
}

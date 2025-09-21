package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mat285/gateway/pkg/kubectl"
	"github.com/mat285/gateway/pkg/log"
	"github.com/mat285/gateway/pkg/proxy"
)

type Config struct {
	ServiceRefreshIntervalSeconds int    `yaml:"serviceRefreshIntervalSeconds"`
	CaddyFilePath                 string `yaml:"caddyFilePath"`
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

	ReverseProxies map[string]ReverseProxyConfig

	Nodes map[string]bool

	cancel context.CancelFunc
	done   chan struct{}
}

func NewServer(config Config) *Server {
	if config.ServiceRefreshIntervalSeconds <= 0 {
		config.ServiceRefreshIntervalSeconds = 30
	}
	if config.CaddyFilePath == "" {
		config.CaddyFilePath = "/etc/caddy/Caddyfile"
	}
	return &Server{
		Lock:           new(sync.Mutex),
		Config:         config,
		TCPProxies:     make(map[string]*proxy.TCPProxy),
		newProxy:       make(chan ProxySpec, 1024),
		ReverseProxies: make(map[string]ReverseProxyConfig),
		Nodes:          make(map[string]bool),
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
	s.fetchServices(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(s.Config.ServiceRefreshIntervalSeconds) * time.Second):
		}
		s.fetchServices(ctx)
	}
}

func (s *Server) fetchServices(ctx context.Context) {
	logger := log.GetLogger(ctx)
	logger.Infof("fetching services")
	namespaces, err := kubectl.GetNamespaces(ctx)
	if err != nil {
		logger.Infof("Error getting namespaces %v", err)
		return
	}
	logger.Infof("namespaces %v", namespaces)
	services, err := kubectl.GetServices(ctx, namespaces...)
	if err != nil {
		logger.Infof("Error getting services %v", err)
		return
	}
	portMap := make(map[string]string)
	reverseProxyMap := make(map[string]ReverseProxyConfig)
	for _, service := range services {
		if service.Spec.Type != "NodePort" {
			continue
		}
		gatewayMap := make(map[string]string)
		portConfigMap := make(map[string]ReverseProxyConfig)
		for annotation, value := range service.Metadata.Annotations {
			if annotation == ServiceAnnotationGatewayTCP {
				parts := strings.Split(value, ":")
				if len(parts) != 2 {
					continue
				}
				logger.Infof("gateway port %s %s", parts[0], parts[1])
				gatewayMap[parts[1]] = parts[0]
				continue
			}
			if annotation == ServiceAnnotationGatewayReverseProxy {
				parts := strings.Split(strings.TrimSpace(value), "|")
				if len(parts) != 3 {
					logger.Infof("invalid reverse proxy config %s", value)
					continue
				}
				reverseProxyConfig := ReverseProxyConfig{
					NodePort:  parts[0],
					Domain:    parts[1],
					HealthURI: parts[2],
				}
				logger.Infof("reverse proxy %s %s", reverseProxyConfig.Domain, reverseProxyConfig.NodePort)
				portConfigMap[reverseProxyConfig.NodePort] = reverseProxyConfig
				continue
			}
		}
		for _, port := range service.Spec.Ports {
			if port.NodePort.StringVal == "" {
				continue
			}
			if _, ok := gatewayMap[port.NodePort.StringVal]; ok {
				logger.Infof("gateway port found for %s", port.NodePort.StringVal)
				portMap[port.NodePort.StringVal] = gatewayMap[port.NodePort.StringVal]
				continue
			}
			if config, ok := portConfigMap[port.NodePort.StringVal]; ok {
				logger.Infof("reverse proxy config found for %s", port.NodePort.StringVal)
				reverseProxyMap[port.NodePort.StringVal] = config
				continue
			}
			logger.Infof("no gateway or reverse proxy config found for %s", port.NodePort.StringVal)
		}
	}
	s.updateServiceProxies(ctx, portMap, reverseProxyMap)
	s.updateCaddy(ctx)
}

func (s *Server) updateServiceProxies(ctx context.Context, tcpPortMap map[string]string, reverseProxyMap map[string]ReverseProxyConfig) {
	logger := log.GetLogger(ctx)
	s.Lock.Lock()
	defer s.Lock.Unlock()
	for nodePort, portName := range tcpPortMap {
		existing, ok := s.TCPProxies[portName]
		if ok && (len(existing.Config.Upstreams) == 0 || !strings.HasSuffix(existing.Config.Upstreams[0], nodePort)) {
			logger.Infof("stopping proxy %s", portName)
			existing.Stop()
			delete(s.TCPProxies, portName)
		}
		logger.Infof("new proxy %s %s", portName, nodePort)
		s.newProxy <- ProxySpec{
			GatewayPort: portName,
			NodePort:    nodePort,
		}
	}
	if areMapsEqual(reverseProxyMap, s.ReverseProxies) {
		logger.Infof("no changes to reverse proxies")
		return
	}
	logger.Infof("updating reverse proxies")
	s.ReverseProxies = reverseProxyMap
}

func (s *Server) updateCaddy(ctx context.Context) {
	s.Lock.Lock()
	defer s.Lock.Unlock()
	logger := log.GetLogger(ctx)
	logger.Infof("updating caddy")
	specs := make([]CaddySpec, 0, len(s.ReverseProxies))
	for nodePort, reverseProxyConfig := range s.ReverseProxies {
		specs = append(specs, CaddySpec{
			Domain:           reverseProxyConfig.Domain,
			ReverseUpstreams: getUpstreamsForNodePort(s.Nodes, nodePort),
			HealthUri:        reverseProxyConfig.HealthURI,
		})
	}
	err := TryUpdateCaddy(ctx, specs, s.Config.CaddyFilePath)
	if err != nil {
		logger.Infof("error updating caddy %v", err)
	}
}

func (s *Server) handleProxies(ctx context.Context) {
	logger := log.GetLogger(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case proxySpec := <-s.newProxy:
			logger.Infof("New proxy %v", proxySpec)
			s.Lock.Lock()
			if _, ok := s.TCPProxies[proxySpec.GatewayPort]; ok {
				s.Lock.Unlock()
				logger.Infof("Proxy already exists %s", proxySpec.GatewayPort)
				continue
			}
			upstreams := make([]string, 0, len(s.Nodes))
			for node := range s.Nodes {
				upstreams = append(upstreams, fmt.Sprintf("%s:%s", node, proxySpec.NodePort))
				logger.Infof("upstream %s %s", node, proxySpec.NodePort)
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

func getUpstreamsForNodePort(nodes map[string]bool, nodePort string) []string {
	upstreams := make([]string, 0, len(nodes))
	for node := range nodes {
		upstreams = append(upstreams, fmt.Sprintf("%s:%s", node, nodePort))
	}
	return upstreams
}

func (s *Server) watchProxy(ctx context.Context, spec ProxySpec, tcpProxy *proxy.TCPProxy) {
	logger := log.GetLogger(ctx)
	defer func() {
		logger.Infof("Stopping TCP proxy %s", tcpProxy.Config.ListenPort)
		s.Lock.Lock()
		delete(s.TCPProxies, tcpProxy.Config.ListenPort)
		s.Lock.Unlock()

		s.cancel()

	}()
	logger.Infof("Starting TCP proxy")
	err := tcpProxy.Start(ctx)
	if err != nil {
		logger.Infof("Error starting TCP proxy %v", err)
	}
}

func (s *Server) watchNodes(ctx context.Context) {
	for {
		s.fetchNodes(ctx)

		select {
		case <-ctx.Done():
			return
		case <-time.After(60 * time.Second):
			continue
		}

	}
}

func (s *Server) fetchNodes(ctx context.Context) {
	logger := log.GetLogger(ctx)
	logger.Infof("fetching nodes")
	currentNodes, err := kubectl.GetNodes(ctx)
	if err != nil {
		logger.Infof("Error getting nodes %v", err)
		return
	}
	replace := make(map[string]bool)
	for _, node := range currentNodes {
		replace[node] = true
	}

	logger.Infof("replacing nodes %v", replace)
	s.Lock.Lock()
	s.Nodes = replace
	s.Lock.Unlock()
}

func areMapsEqual(a, b map[string]ReverseProxyConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

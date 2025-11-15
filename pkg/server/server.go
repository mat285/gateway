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
	"github.com/mat285/gateway/pkg/wait"
)

type Config struct {
	ServiceRefreshIntervalSeconds int    `yaml:"serviceRefreshIntervalSeconds"`
	CaddyFilePath                 string `yaml:"caddyFilePath"`
	CaddyTLSCertificateDirectory  string `yaml:"caddyTLSCertificateDirectory"`
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
	logger := log.GetLogger(ctx)
	ctx, s.cancel = context.WithCancel(ctx)
	defer s.cancel()
	s.done = make(chan struct{})
	defer close(s.done)

	wg := wait.NewGroup()
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

	err := wg.WaitContext(ctx)
	if err != nil {
		logger.Infof("error waiting for exit %v", err)
	}
	logger.Infof("Gateway server stopped")
	return err
}

func (s *Server) watchServices(ctx context.Context) {
	logger := log.GetLogger(ctx)
	logger.Infof("starting service watcher")
	s.fetchServices(ctx)
	for {
		select {
		case <-ctx.Done():
			logger.Infof("stopping service watcher")
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
				gatewayMap[parts[0]] = parts[1]
				continue
			}
			if annotation == ServiceAnnotationGatewayReverseProxy {
				parts := strings.Split(strings.TrimSpace(value), "|")
				if len(parts) != 3 {
					logger.Infof("invalid reverse proxy config %s", value)
					continue
				}
				reverseProxyConfig := ReverseProxyConfig{
					NodePort:           parts[0],
					Domain:             parts[1],
					HealthURI:          parts[2],
					TLSSecretNamespace: service.Metadata.Namespace,
					TLSSecretName:      service.Metadata.Annotations[ServiceAnnotationGatewayTLSSecret],
				}
				logger.Infof("reverse proxy %s %s", reverseProxyConfig.Domain, reverseProxyConfig.NodePort)
				portConfigMap[reverseProxyConfig.NodePort] = reverseProxyConfig
				continue
			}
		}
		if len(gatewayMap) == 0 && len(portConfigMap) == 0 {
			logger.Infof("no gateway or reverse proxy config found for service %s/%s", service.Metadata.Namespace, service.Metadata.Name)
			continue
		}
		for _, port := range service.Spec.Ports {
			if port.NodePort == 0 {
				continue
			}
			strVal := fmt.Sprintf("%d", port.NodePort)
			if gatewayPort, ok := gatewayMap[strVal]; ok {
				logger.Infof("gateway port found for service %s/%s port %s -> %s", service.Metadata.Namespace, service.Metadata.Name, strVal, gatewayPort)
				portMap[gatewayPort] = strVal
				continue
			}
			if config, ok := portConfigMap[strVal]; ok {
				logger.Infof("reverse proxy config found for service %s/%s port %s", service.Metadata.Namespace, service.Metadata.Name, strVal)
				reverseProxyMap[strVal] = config
				continue
			}
			logger.Infof("no gateway or reverse proxy config found for service %s/%s port %s", service.Metadata.Namespace, service.Metadata.Name, strVal)
		}
	}
	s.updateServiceProxies(ctx, portMap, reverseProxyMap)
	s.updateCaddy(ctx)
}

func (s *Server) updateServiceProxies(ctx context.Context, tcpPortMap map[string]string, reverseProxyMap map[string]ReverseProxyConfig) {
	logger := log.GetLogger(ctx)
	logger.Infof("updating service proxies")
	s.Lock.Lock()
	defer s.Lock.Unlock()
	for gatewayPort, nodePort := range tcpPortMap {
		logger.Infof("updating tcp service proxy forwarding gateway %s -> %s node port", gatewayPort, nodePort)
		existing, ok := s.TCPProxies[gatewayPort]
		if ok && (len(existing.Config.Upstreams) == 0 || !strings.HasSuffix(existing.Config.Upstreams[0], nodePort)) {
			// logger.Infof("stopping proxy %s", gatewayPort)
			// existing.Stop()
			// delete(s.TCPProxies, gatewayPort)[
			logger.Infof("proxy already exists for gateway %s -> %s", gatewayPort, nodePort)
			continue
		}
		logger.Infof("starting tcp service proxy %s -> %s", gatewayPort, nodePort)
		s.newProxy <- ProxySpec{
			GatewayPort: gatewayPort,
			NodePort:    nodePort,
		}
	}

	logger.Infof("checking if reverse proxies have changed")
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
			Domain:             reverseProxyConfig.Domain,
			ReverseUpstreams:   getUpstreamsForNodePort(s.Nodes, nodePort),
			HealthUri:          reverseProxyConfig.HealthURI,
			TLSSecretName:      reverseProxyConfig.TLSSecretName,
			TLSSecretNamespace: reverseProxyConfig.TLSSecretNamespace,
		})
	}
	nodes := make([]string, 0, len(s.Nodes))
	for node := range s.Nodes {
		nodes = append(nodes, node)
	}
	err := TryUpdateCaddy(ctx, specs, nodes, s.Config.CaddyFilePath, s.Config.CaddyTLSCertificateDirectory)
	if err != nil {
		logger.Infof("error updating caddy %v", err)
	}
}

func (s *Server) handleProxies(ctx context.Context) {
	logger := log.GetLogger(ctx)
	for {
		select {
		case <-ctx.Done():
			logger.Infof("stopping proxy handler")
			s.Lock.Lock()
			defer s.Lock.Unlock()
			for _, tcpProxy := range s.TCPProxies {
				tcpProxy.Stop()
			}
			return
		case proxySpec := <-s.newProxy:
			logger.Infof("New proxy from %s -> %s", proxySpec.GatewayPort, proxySpec.NodePort)
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
		logger.Infof("Stopping TCP proxy %s -> %s", tcpProxy.Config.ListenPort, spec.NodePort)
		s.Lock.Lock()
		delete(s.TCPProxies, tcpProxy.Config.ListenPort)
		s.Lock.Unlock()
		s.newProxy <- spec
	}()
	logger.Infof("Starting TCP proxy %s -> %s", tcpProxy.Config.ListenPort, spec.NodePort)
	err := tcpProxy.Start(ctx)
	if err != nil {
		logger.Infof("Error starting TCP proxy %v", err)
	}
}

func (s *Server) watchNodes(ctx context.Context) {
	logger := log.GetLogger(ctx)
	logger.Infof("starting node watcher")
	for {
		s.fetchNodes(ctx)

		select {
		case <-ctx.Done():
			logger.Infof("stopping node watcher")
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

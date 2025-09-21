package server

const ServiceAnnotationGatewayTCP = "gateway.nori.ninja/tcp"
const ServiceAnnotationGatewayReverseProxy = "gateway.nori.ninja/reverse-proxy"

type ReverseProxyConfig struct {
	Domain    string `json:"domain"`
	HealthURI string `json:"healthURI"`
	NodePort  string `json:"nodePort"`
}

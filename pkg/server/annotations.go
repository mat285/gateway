package server

const ServiceAnnotationGatewayTCP = "gateway.nori.ninja/tcp"
const ServiceAnnotationGatewayReverseProxy = "gateway.nori.ninja/reverse-proxy"
const ServiceAnnotationGatewayTLSSecret = "gateway.nori.ninja/tls-secret"

type ReverseProxyConfig struct {
	Domain             string `json:"domain"`
	HealthURI          string `json:"healthURI"`
	NodePort           string `json:"nodePort"`
	TLSSecretName      string `json:"tlsSecretName"`
	TLSSecretNamespace string `json:"tlsSecretNamespace"`
}

package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mat285/gateway/pkg/kubectl"
	"github.com/mat285/gateway/pkg/log"
)

const (
	defaultCaddyCertificateDirectory = "/var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/"

	caddyGlobalStanzaTemplate = `
{
        acme_ca https://acme-v02.api.letsencrypt.org/directory
}

%s
`
	caddyFileStanzaTemplate = `%s {
    reverse_proxy {
            to %s

            lb_policy round_robin
            lb_retries 2

            health_uri %s
            health_interval 5s
            health_timeout 2s
            health_status 2xx
	}
}
`

	caddyNodeCheckStanzaTemplate = `
%s.health.k8s.nori.ninja {
        reverse_proxy %s:30888
}
`
)

type CaddySpec struct {
	Domain             string
	ReverseUpstreams   []string
	HealthUri          string
	TLSSecretName      string
	TLSSecretNamespace string
}

func GenerateCaddyfile(ctx context.Context, reverseUpstreams []CaddySpec, nodes []string) (string, error) {
	stanzas := make([]string, 0, len(reverseUpstreams))
	for _, caddySpec := range reverseUpstreams {
		stanzas = append(stanzas, GenerateCaddyfileStanza(caddySpec.Domain, caddySpec.ReverseUpstreams, caddySpec.HealthUri))
	}
	for _, node := range nodes {
		stanzas = append(stanzas, fmt.Sprintf(caddyNodeCheckStanzaTemplate, node, node))
	}
	sort.Strings(stanzas)
	return fmt.Sprintf(caddyGlobalStanzaTemplate, strings.Join(stanzas, "\n")), nil
}

func GenerateCaddyfileStanza(domain string, reverseUpstreams []string, healthUri string) string {
	sort.Strings(reverseUpstreams)
	return fmt.Sprintf(caddyFileStanzaTemplate, domain, strings.Join(reverseUpstreams, " "), healthUri)
}

func TryUpdateCaddy(ctx context.Context, caddySpec []CaddySpec, nodes []string, caddyFilePath, caddyTLSCertificateDirectory string) error {
	logger := log.GetLogger(ctx)
	if len(caddyTLSCertificateDirectory) == 0 {
		caddyTLSCertificateDirectory = defaultCaddyCertificateDirectory
	}
	logger.Infof("updating caddy")
	updatedTLSCertificates, err := TryUpdateCaddyTLSCertificates(ctx, caddySpec, caddyTLSCertificateDirectory)
	if err != nil {
		logger.Infof("error updating caddy tls certificates %v", err)
		return err
	}
	updatedCaddyFile, err := TryUpdateCaddyFile(ctx, caddySpec, nodes, caddyFilePath)
	if err != nil {
		logger.Infof("error updating caddyfile %v", err)
		return err
	}
	if updatedTLSCertificates || updatedCaddyFile {
		logger.Infof("restarting caddy")
		return RestartCaddy(ctx)
	}
	return nil
}

func TryUpdateCaddyFile(ctx context.Context, caddySpec []CaddySpec, nodes []string, caddyFilePath string) (bool, error) {
	logger := log.GetLogger(ctx)
	logger.Infof("updating caddyfile from")
	existing, err := os.ReadFile(caddyFilePath)
	if err != nil {
		return false, err
	}
	caddyfile, err := GenerateCaddyfile(ctx, caddySpec, nodes)
	if err != nil {
		logger.Infof("error generating caddyfile %v", err)
		return false, err
	}
	if string(existing) == caddyfile {
		logger.Infof("no changes to caddyfile")
		return false, nil
	}
	// fmt.Println("--------------------------------")
	// fmt.Println(caddyfile)
	// fmt.Println("--------------------------------")
	// fmt.Println(string(existing))
	logger.Infof("updating caddyfile from %s to %s", existing, caddyfile)

	logger.Infof("writing caddyfile to %s", caddyFilePath)
	err = os.WriteFile(caddyFilePath, []byte(caddyfile), 0644)
	if err != nil {
		logger.Infof("error writing caddyfile %v", err)
		return false, err
	}
	logger.Infof("restarting caddy")
	return true, nil
}

func RestartCaddy(ctx context.Context) error {
	return exec.CommandContext(ctx, "systemctl", "restart", "caddy").Run()
}

func TryUpdateCaddyTLSCertificates(ctx context.Context, caddyTLSSpecs []CaddySpec, caddyTLSCertificateDirectory string) (bool, error) {
	logger := log.GetLogger(ctx)
	logger.Infof("trying to update caddy tls certificates")
	needsRestart := false
	for _, caddyTLSSpec := range caddyTLSSpecs {
		if len(caddyTLSSpec.TLSSecretName) == 0 {
			logger.Infof("tls secret name is empty for domain %s", caddyTLSSpec.Domain)
			continue
		}
		cert, key, err := GetTLSCertificateFromSecret(ctx, caddyTLSSpec.TLSSecretName, caddyTLSSpec.TLSSecretNamespace)
		if err != nil {
			logger.Infof("error getting secret %s in namespace %s %v", caddyTLSSpec.TLSSecretName, caddyTLSSpec.TLSSecretNamespace, err)
			continue
		}
		logger.Infof("trying to update caddy tls certificate for domain %s", caddyTLSSpec.Domain)
		updated, err := TryUpdateCaddyTLSCertificate(ctx, caddyTLSSpec.Domain, cert, key, caddyTLSCertificateDirectory)
		if err != nil {
			logger.Infof("error updating caddy tls certificate for domain %s %v", caddyTLSSpec.Domain, err)
			continue
		}
		logger.Infof("caddy tls certificate for domain %s updated %t", caddyTLSSpec.Domain, updated)
		if updated {
			logger.Infof("caddy tls certificate for domain %s updated successfully", caddyTLSSpec.Domain)
			needsRestart = true
		}
	}
	return needsRestart, nil
}

func GetTLSCertificateFromSecret(ctx context.Context, name, namespace string) ([]byte, []byte, error) {
	if len(name) == 0 {
		return nil, nil, fmt.Errorf("tls secret name is empty")
	}
	secret, err := kubectl.GetSecret(ctx, name, namespace)
	if err != nil {
		return nil, nil, err
	}
	cert, ok := secret.Data["tls.crt"]
	if !ok {
		return nil, nil, fmt.Errorf("secret %s in namespace %s does not contain tls.crt", name, namespace)
	}
	key, ok := secret.Data["tls.key"]
	if !ok {
		return nil, nil, fmt.Errorf("secret %s in namespace %s does not contain tls.key", name, namespace)
	}
	return cert, key, nil
}

func TryUpdateCaddyTLSCertificate(ctx context.Context, domain string, cert []byte, key []byte, caddyTLSCertificateDirectory string) (bool, error) {
	logger := log.GetLogger(ctx)
	domainDirectory := CaddyTLSCertificateDomainDirectory(domain, caddyTLSCertificateDirectory)
	_, err := os.Stat(domainDirectory)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		logger.Infof("creating caddy tls certificate directory %s", domainDirectory)
		err = os.MkdirAll(domainDirectory, 0755)
		if err != nil {
			logger.Infof("error creating caddy tls certificate directory %s %v", domainDirectory, err)
			return false, err
		}
	}
	certFilePath := CaddyTLSCertFilePath(domain, caddyTLSCertificateDirectory)
	keyFilePath := CaddyTLSPrivateKeyFilePath(domain, caddyTLSCertificateDirectory)
	existingCert, err := os.ReadFile(certFilePath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	existingKey, err := os.ReadFile(keyFilePath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if bytes.Equal(existingCert, cert) && bytes.Equal(existingKey, key) {
		return false, nil
	}
	err = os.WriteFile(certFilePath, cert, 0644)
	if err != nil {
		return true, err
	}
	err = os.WriteFile(keyFilePath, key, 0644)
	if err != nil {
		os.WriteFile(certFilePath, existingCert, 0644)
		return true, err
	}
	return true, nil
}

func CaddyTLSCertificateDomainDirectory(domain string, caddyTLSCertificateDirectory string) string {
	return filepath.Join(caddyTLSCertificateDirectory, domain)
}

func CaddyTLSCertFilePath(domain string, caddyTLSCertificateDirectory string) string {
	return filepath.Join(CaddyTLSCertificateDomainDirectory(domain, caddyTLSCertificateDirectory), fmt.Sprintf("%s.crt", domain))
}

func CaddyTLSPrivateKeyFilePath(domain string, caddyTLSCertificateDirectory string) string {
	return filepath.Join(CaddyTLSCertificateDomainDirectory(domain, caddyTLSCertificateDirectory), fmt.Sprintf("%s.key", domain))
}

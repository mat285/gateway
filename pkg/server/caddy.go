package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/mat285/gateway/pkg/log"
)

const (
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
	Domain           string
	ReverseUpstreams []string
	HealthUri        string
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

func TryUpdateCaddy(ctx context.Context, caddySpec []CaddySpec, nodes []string, caddyFilePath string) error {
	logger := log.GetLogger(ctx)
	logger.Infof("updating caddyfile from")
	existing, err := os.ReadFile(caddyFilePath)
	if err != nil {
		return err
	}
	caddyfile, err := GenerateCaddyfile(ctx, caddySpec, nodes)
	if err != nil {
		logger.Infof("error generating caddyfile %v", err)
		return err
	}
	if string(existing) == caddyfile {
		logger.Infof("no changes to caddyfile")
		return nil
	}
	fmt.Println("--------------------------------")
	fmt.Println(caddyfile)
	fmt.Println("--------------------------------")
	fmt.Println(string(existing))
	logger.Infof("updating caddyfile from %s to %s", existing, caddyfile)

	logger.Infof("writing caddyfile to %s", caddyFilePath)
	err = os.WriteFile(caddyFilePath, []byte(caddyfile), 0644)
	if err != nil {
		logger.Infof("error writing caddyfile %v", err)
		return err
	}
	logger.Infof("restarting caddy")
	return nil
	err = RestartCaddy(ctx)
	if err != nil {
		logger.Infof("error restarting caddy %v reverting and restarting caddy", err)
		revErr := os.WriteFile(caddyFilePath, existing, 0644)
		if revErr != nil {
			logger.Infof("error reverting caddyfile %v: %v", revErr, err)
			return fmt.Errorf("error reverting caddyfile %v: %v", revErr, err)
		}
		caddyErr := RestartCaddy(ctx)
		if caddyErr != nil {
			logger.Infof("error restarting caddy %v: %v", caddyErr, err)
			return fmt.Errorf("error restarting caddy %v: %v", caddyErr, err)
		}
		return fmt.Errorf("error restarting caddy %v: %v", caddyErr, err)
	}
	logger.Infof("caddyfile updated successfully")
	return nil
}

func RestartCaddy(ctx context.Context) error {
	return exec.CommandContext(ctx, "systemctl", "restart", "caddy").Run()
}

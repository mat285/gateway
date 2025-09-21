package kubectl

import (
	"context"
	"os/exec"
	"strings"
)

func GetNamespaces(ctx context.Context) ([]string, error) {
	output, err := exec.CommandContext(ctx, "kubectl", "get", "namespaces", "-o", `jsonpath="{.items[*].metadata.name}"`).Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(string(output)), " "), nil
}

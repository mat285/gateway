package kubectl

import (
	"context"
	"os/exec"
	"strings"
)

func GetNodes(ctx context.Context) ([]string, error) {
	output, err := exec.CommandContext(ctx, "kubectl", "get", "nodes", "-o", `jsonpath="{.items[*].metadata.name}"`).Output()
	if err != nil {
		return nil, err
	}
	out := strings.TrimSpace(string(output))
	out = strings.Trim(out, "\"")
	out = strings.TrimSpace(out)
	nodes := strings.Split(out, " ")
	return nodes, nil
}

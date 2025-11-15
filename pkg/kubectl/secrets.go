package kubectl

import (
	"context"
	"encoding/json"
	"os/exec"
)

type Secret struct {
	Data map[string][]byte `json:"data"`
}

func GetSecret(ctx context.Context, name string, namespace string) (*Secret, error) {
	output, err := exec.CommandContext(ctx, "kubectl", "get", "secret", name, "-n", namespace, "-o", "json").Output()
	if err != nil {
		return nil, err
	}
	var secret Secret
	err = json.Unmarshal(output, &secret)
	if err != nil {
		return nil, err
	}
	return &secret, nil
}

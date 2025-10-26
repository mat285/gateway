package kubectl

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type ServiceList struct {
	Items []Service `json:"items"`
}

type Service struct {
	Metadata ServiceMetadata `json:"metadata"`
	Spec     ServiceSpec     `json:"spec"`
}

type ServiceMetadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Annotations map[string]string `json:"annotations"`
}

type ServiceSpec struct {
	Type  string        `json:"type"`
	Ports []ServicePort `json:"ports"`
}

type ServicePort struct {
	Name     string `json:"name"`
	NodePort int    `json:"nodePort"`
}

type IntOrString struct {
	IntVal    int
	StringVal string
}

func (i *IntOrString) UnmarshalJSON(data []byte) error {
	err := json.Unmarshal(data, &i.StringVal)
	if err != nil {
		err = json.Unmarshal(data, &i.IntVal)
		if err != nil {
			return err
		}
		i.StringVal = fmt.Sprintf("%d", i.IntVal)
	}
	return nil
}

func GetServices(ctx context.Context, namespaces ...string) ([]Service, error) {
	results := []Service{}
	for _, namespace := range namespaces {
		services, err := GetServicesInNamespace(ctx, namespace)
		if err != nil {
			return nil, err
		}
		results = append(results, services...)
	}
	return results, nil

}

func GetServicesInNamespace(ctx context.Context, namespace string) ([]Service, error) {
	output, err := exec.CommandContext(ctx, "kubectl", "get", "services", "-n", namespace, "-o", "json").Output()
	if err != nil {
		return nil, err
	}
	var services ServiceList
	err = json.Unmarshal(output, &services)
	if err != nil {
		return nil, err
	}
	return services.Items, nil
}

package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"sigs.k8s.io/yaml"
)

const (
	DiscoveryLabel   = "models.sberdevices.ru/discovery"
	AliasAnnotation  = "models.sberdevices.ru/alias"
	FilterAnnotation = "models.sberdevices.ru/model-filter"
	SchemeAnnotation = "models.sberdevices.ru/scheme"
	APIPortName      = "vllm-http"
)

type model struct {
	Name   string    `json:"model_name"`
	Params params    `json:"litellm_params"`
	Info   modelInfo `json:"model_info"`
}
type params struct {
	Model   string `json:"model"`
	APIBase string `json:"api_base"`
	APIKey  string `json:"api_key"`
}
type modelInfo struct {
	SourceService string `json:"source_service"`
}

type DiscoveredModel struct {
	Name, UpstreamModel, APIBase, SourceService string
}

// Generate merges the vLLM-discovered model_list into a user-managed base
// LiteLLM config. model_list is exclusively adapter-owned.
func Generate(base []byte, discovered []DiscoveredModel) ([]byte, string, error) {
	config := map[string]interface{}{}
	if err := yaml.Unmarshal(base, &config); err != nil {
		return nil, "", fmt.Errorf("parse base config: %w", err)
	}
	if config == nil {
		config = map[string]interface{}{}
	}
	if _, exists := config["model_list"]; exists {
		return nil, "", fmt.Errorf("base config must not contain model_list: it is managed by the adapter")
	}
	seen := map[string]string{}
	models := make([]model, 0, len(discovered))
	for _, candidate := range discovered {
		if candidate.Name == "" || candidate.UpstreamModel == "" || candidate.APIBase == "" {
			return nil, "", fmt.Errorf("discovered model from %s has empty name, upstream model or api_base", candidate.SourceService)
		}
		if previous, ok := seen[candidate.Name]; ok {
			return nil, "", fmt.Errorf("duplicate model %q on Services %s and %s", candidate.Name, previous, candidate.SourceService)
		}
		seen[candidate.Name] = candidate.SourceService
		models = append(models, model{Name: candidate.Name, Params: params{Model: "openai/" + candidate.UpstreamModel, APIBase: candidate.APIBase, APIKey: "EMPTY"}, Info: modelInfo{SourceService: candidate.SourceService}})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	config["model_list"] = models
	out, err := yaml.Marshal(config)
	if err != nil {
		return nil, "", fmt.Errorf("marshal config: %w", err)
	}
	sum := sha256.Sum256(out)
	return out, hex.EncodeToString(sum[:]), nil
}

func ServiceURL(svc *corev1.Service, scheme string, port int32) string {
	return fmt.Sprintf("%s://%s.%s.svc.cluster.local:%d", scheme, svc.Name, svc.Namespace, port)
}

func ServiceEndpoint(svc *corev1.Service) (string, error) {
	port, err := servicePort(svc)
	if err != nil {
		return "", err
	}
	scheme := svc.Annotations[SchemeAnnotation]
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("Service %s/%s: unsupported scheme %q", svc.Namespace, svc.Name, scheme)
	}
	return ServiceURL(svc, scheme, port), nil
}

func HasReadyEndpoint(namespace, serviceName string, slices []discoveryv1.EndpointSlice) bool {
	return readyServices(slices)[namespace+"/"+serviceName]
}

func readyServices(slices []discoveryv1.EndpointSlice) map[string]bool {
	result := map[string]bool{}
	for i := range slices {
		name := slices[i].Labels[discoveryv1.LabelServiceName]
		key := slices[i].Namespace + "/" + name
		for _, endpoint := range slices[i].Endpoints {
			// nil means "unknown" and is treated as ready by Kubernetes clients.
			if endpoint.Conditions.Ready == nil || *endpoint.Conditions.Ready {
				result[key] = true
				break
			}
		}
	}
	return result
}

func servicePort(svc *corev1.Service) (int32, error) {
	for _, port := range svc.Spec.Ports {
		if port.Name == APIPortName {
			return port.Port, nil
		}
	}
	return 0, fmt.Errorf("Service %s/%s: required API port %q is missing", svc.Namespace, svc.Name, APIPortName)
}

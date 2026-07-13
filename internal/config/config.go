package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"sigs.k8s.io/yaml"
)

const (
	DiscoveryLabel   = "models.sberdevices.ru/discovery"
	NameLabel        = "models.sberdevices.ru/name"
	VersionLabel     = "models.sberdevices.ru/version"
	PortAnnotation   = "models.sberdevices.ru/port"
	SchemeAnnotation = "models.sberdevices.ru/scheme"
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
	Version string `json:"version"`
}

// Generate merges the discovered model_list into a user-managed base LiteLLM config.
// Services without a ready endpoint are omitted. model_list is exclusively adapter-owned.
func Generate(base []byte, services []corev1.Service, slices []discoveryv1.EndpointSlice) ([]byte, string, error) {
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
	ready := readyServices(slices)
	seen := map[string]string{}
	models := make([]model, 0, len(services))
	for i := range services {
		svc := &services[i]
		if svc.Labels[DiscoveryLabel] != "litellm" || !ready[svc.Name] {
			continue
		}
		name := strings.TrimSpace(svc.Labels[NameLabel])
		version := strings.TrimSpace(svc.Labels[VersionLabel])
		if name == "" || version == "" {
			return nil, "", fmt.Errorf("Service %s/%s: %s and %s are required", svc.Namespace, svc.Name, NameLabel, VersionLabel)
		}
		if previous, ok := seen[name]; ok {
			return nil, "", fmt.Errorf("duplicate model %q on Services %s and %s", name, previous, svc.Name)
		}
		seen[name] = svc.Name
		port, err := servicePort(svc)
		if err != nil {
			return nil, "", err
		}
		scheme := svc.Annotations[SchemeAnnotation]
		if scheme == "" {
			scheme = "http"
		}
		if scheme != "http" && scheme != "https" {
			return nil, "", fmt.Errorf("Service %s/%s: unsupported scheme %q", svc.Namespace, svc.Name, scheme)
		}
		base := fmt.Sprintf("%s://%s.%s.svc.cluster.local:%d/v1", scheme, svc.Name, svc.Namespace, port)
		models = append(models, model{Name: name, Params: params{Model: "openai/" + name, APIBase: base, APIKey: "EMPTY"}, Info: modelInfo{Version: version}})
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

func readyServices(slices []discoveryv1.EndpointSlice) map[string]bool {
	result := map[string]bool{}
	for i := range slices {
		name := slices[i].Labels[discoveryv1.LabelServiceName]
		for _, endpoint := range slices[i].Endpoints {
			// nil means "unknown" and is treated as ready by Kubernetes clients.
			if endpoint.Conditions.Ready == nil || *endpoint.Conditions.Ready {
				result[name] = true
				break
			}
		}
	}
	return result
}

func servicePort(svc *corev1.Service) (int32, error) {
	if raw := svc.Annotations[PortAnnotation]; raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 32); err == nil && n > 0 && n <= 65535 {
			return int32(n), nil
		}
		for _, p := range svc.Spec.Ports {
			if p.Name == raw {
				return p.Port, nil
			}
		}
		return 0, fmt.Errorf("Service %s/%s: annotation %s=%q is not a port number or name", svc.Namespace, svc.Name, PortAnnotation, raw)
	}
	if len(svc.Spec.Ports) == 0 {
		return 0, fmt.Errorf("Service %s/%s has no ports", svc.Namespace, svc.Name)
	}
	return svc.Spec.Ports[0].Port, nil
}

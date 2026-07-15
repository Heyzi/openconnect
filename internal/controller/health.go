package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	adapterconfig "gitlab.sberdevices.ru/rndml/devops/litellm-configurator/internal/config"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"sigs.k8s.io/yaml"
)

type serviceHealthChecker interface {
	Check(context.Context, *corev1.Service) ([]string, error)
}

type httpHealthChecker struct{ client *http.Client }

func (c httpHealthChecker) Check(ctx context.Context, svc *corev1.Service) ([]string, error) {
	base, err := adapterconfig.ServiceEndpoint(svc)
	if err != nil {
		return nil, err
	}
	if err := c.getOK(ctx, base+"/health"); err != nil {
		return nil, fmt.Errorf("health endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("models endpoint returned %s", resp.Status)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	ids := make([]string, 0, len(payload.Data))
	seen := map[string]bool{}
	for _, model := range payload.Data {
		id := strings.TrimSpace(model.ID)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("vLLM returned no models")
	}
	sort.Strings(ids)
	return ids, nil
}

func (c httpHealthChecker) getOK(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("returned %s", resp.Status)
	}
	return nil
}

type serviceState struct {
	observedSet          string
	consecutiveSuccesses int
	lastHealthy          time.Time
	published            []adapterconfig.DiscoveredModel
}

type discoveryTracker struct {
	checker          serviceHealthChecker
	successThreshold int
	unhealthyGrace   time.Duration
	now              func() time.Time
	states           map[string]*serviceState
}

func newDiscoveryTracker(checker serviceHealthChecker, successThreshold int, unhealthyGrace time.Duration) *discoveryTracker {
	return &discoveryTracker{checker: checker, successThreshold: successThreshold, unhealthyGrace: unhealthyGrace, now: time.Now, states: map[string]*serviceState{}}
}

// Filter returns the stable model set. A changed /v1/models response must be
// observed successThreshold times before it replaces the published set.
func (t *discoveryTracker) Filter(ctx context.Context, services []corev1.Service, slices []discoveryv1.EndpointSlice, generatedConfig string) (models []adapterconfig.DiscoveredModel, pending bool, err error) {
	now := t.now()
	previouslyPublished := modelsByService(generatedConfig)
	present := make(map[string]bool, len(services))
	for i := range services {
		svc := &services[i]
		key := svc.Namespace + "/" + svc.Name
		present[key] = true
		state := t.states[key]
		if state == nil {
			state = &serviceState{published: previouslyPublished[key]}
			if len(state.published) > 0 {
				state.lastHealthy = now
			}
			t.states[key] = state
		}

		if _, endpointErr := adapterconfig.ServiceEndpoint(svc); endpointErr != nil {
			return nil, false, endpointErr
		}
		var ids []string
		var healthErr error
		if adapterconfig.HasReadyEndpoint(svc.Namespace, svc.Name, slices) {
			ids, healthErr = t.checker.Check(ctx, svc)
		} else {
			healthErr = fmt.Errorf("no ready EndpointSlice endpoint")
		}
		if healthErr == nil {
			candidates, candidateErr := discoveredModels(svc, ids)
			if candidateErr != nil {
				return nil, false, candidateErr
			}
			setKey := discoveredSetKey(candidates)
			if setKey == state.observedSet {
				state.consecutiveSuccesses++
			} else {
				state.observedSet = setKey
				state.consecutiveSuccesses = 1
			}
			state.lastHealthy = now
			if state.consecutiveSuccesses >= t.successThreshold {
				state.published = candidates
			} else if !sameDiscoveredModels(state.published, candidates) {
				pending = true
			}
		} else {
			state.consecutiveSuccesses = 0
			state.observedSet = ""
			slog.Warn("vLLM service is not healthy", "service", key, "error", healthErr)
			if len(state.published) > 0 && now.Sub(state.lastHealthy) >= t.unhealthyGrace {
				state.published = nil
			}
		}
		models = append(models, state.published...)
	}
	for key := range t.states {
		if !present[key] {
			delete(t.states, key)
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, pending, nil
}

func discoveredModels(svc *corev1.Service, ids []string) ([]adapterconfig.DiscoveredModel, error) {
	filter := svc.Annotations[adapterconfig.FilterAnnotation]
	var re *regexp.Regexp
	var err error
	if filter != "" {
		re, err = regexp.Compile(filter)
		if err != nil {
			return nil, fmt.Errorf("Service %s/%s: invalid %s: %w", svc.Namespace, svc.Name, adapterconfig.FilterAnnotation, err)
		}
	}
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		if re == nil || re.MatchString(id) {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("Service %s/%s: model filter matched no vLLM models", svc.Namespace, svc.Name)
	}
	alias := strings.TrimSpace(svc.Annotations[adapterconfig.AliasAnnotation])
	if alias != "" && len(filtered) != 1 {
		return nil, fmt.Errorf("Service %s/%s: %s requires exactly one discovered model, got %d", svc.Namespace, svc.Name, adapterconfig.AliasAnnotation, len(filtered))
	}
	base, err := adapterconfig.ServiceEndpoint(svc)
	if err != nil {
		return nil, err
	}
	result := make([]adapterconfig.DiscoveredModel, 0, len(filtered))
	for _, id := range filtered {
		name := id
		if alias != "" {
			name = alias
		}
		result = append(result, adapterconfig.DiscoveredModel{Name: name, UpstreamModel: id, APIBase: base + "/v1", SourceService: svc.Namespace + "/" + svc.Name})
	}
	return result, nil
}

func discoveredSetKey(models []adapterconfig.DiscoveredModel) string {
	parts := make([]string, len(models))
	for i, model := range models {
		parts[i] = model.Name + "\x00" + model.UpstreamModel
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

func sameDiscoveredModels(a, b []adapterconfig.DiscoveredModel) bool {
	return discoveredSetKey(a) == discoveredSetKey(b)
}

func modelsByService(config string) map[string][]adapterconfig.DiscoveredModel {
	result := map[string][]adapterconfig.DiscoveredModel{}
	if strings.TrimSpace(config) == "" {
		return result
	}
	var parsed struct {
		Models []struct {
			Name   string `json:"model_name"`
			Params struct {
				Model   string `json:"model"`
				APIBase string `json:"api_base"`
			} `json:"litellm_params"`
			Info struct {
				SourceService string `json:"source_service"`
			} `json:"model_info"`
		} `json:"model_list"`
	}
	if err := yaml.Unmarshal([]byte(config), &parsed); err != nil {
		return result
	}
	for _, model := range parsed.Models {
		sourceService := model.Info.SourceService
		if sourceService == "" {
			if parsedURL, err := url.Parse(model.Params.APIBase); err == nil {
				hostParts := strings.Split(parsedURL.Hostname(), ".")
				if len(hostParts) >= 2 {
					sourceService = hostParts[1] + "/" + hostParts[0]
				}
			}
		}
		if sourceService == "" {
			continue
		}
		result[sourceService] = append(result[sourceService], adapterconfig.DiscoveredModel{
			Name: model.Name, UpstreamModel: strings.TrimPrefix(model.Params.Model, "openai/"), APIBase: model.Params.APIBase, SourceService: sourceService,
		})
	}
	return result
}

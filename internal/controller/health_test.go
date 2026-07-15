package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	adapterconfig "gitlab.sberdevices.ru/rndml/devops/litellm-configurator/internal/config"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeHealthChecker struct {
	healthy bool
	models  []string
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type healthCheckerFunc func(context.Context, *corev1.Service) ([]string, error)

func (f healthCheckerFunc) Check(ctx context.Context, svc *corev1.Service) ([]string, error) {
	return f(ctx, svc)
}

func (c *fakeHealthChecker) Check(context.Context, *corev1.Service) ([]string, error) {
	if !c.healthy {
		return nil, fmt.Errorf("unhealthy")
	}
	return c.models, nil
}

func TestDiscoveryTrackerStabilizesAdditionAndTemporaryFailure(t *testing.T) {
	checker := &fakeHealthChecker{healthy: true, models: []string{"org/model", "org/lora"}}
	tracker := newDiscoveryTracker(checker, 2, 2*time.Minute)
	now := time.Unix(1000, 0)
	tracker.now = func() time.Time { return now }
	services := []corev1.Service{trackedService()}
	slices := []discoveryv1.EndpointSlice{trackedReadySlice()}

	filtered, pending, err := tracker.Filter(context.Background(), services, slices, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 || !pending {
		t.Fatalf("first successful check must be pending, got services=%d pending=%v", len(filtered), pending)
	}
	filtered, pending, err = tracker.Filter(context.Background(), services, slices, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 || pending {
		t.Fatalf("second successful check must publish, got services=%d pending=%v", len(filtered), pending)
	}

	checker.healthy = false
	now = now.Add(time.Minute)
	filtered, _, _ = tracker.Filter(context.Background(), services, slices, "")
	if len(filtered) != 2 {
		t.Fatal("temporarily unhealthy published service was removed before grace period")
	}
	now = now.Add(2 * time.Minute)
	filtered, _, _ = tracker.Filter(context.Background(), services, slices, "")
	if len(filtered) != 0 {
		t.Fatal("unhealthy service was retained after grace period")
	}
}

func TestDiscoveryTrackerBootstrapsPublishedStateAndDeletesExplicitly(t *testing.T) {
	checker := &fakeHealthChecker{healthy: false}
	tracker := newDiscoveryTracker(checker, 2, 2*time.Minute)
	tracker.now = func() time.Time { return time.Unix(1000, 0) }
	generated := "model_list:\n  - model_name: org/model\n    litellm_params:\n      model: openai/org/model\n      api_base: http://vllm.test.svc.cluster.local:8000/v1\n    model_info:\n      source_service: test/vllm\n"
	filtered, pending, err := tracker.Filter(context.Background(), []corev1.Service{trackedService()}, []discoveryv1.EndpointSlice{trackedReadySlice()}, generated)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || pending {
		t.Fatalf("existing generated model must survive adapter restart and grace period, got services=%d pending=%v", len(filtered), pending)
	}
	filtered, pending, err = tracker.Filter(context.Background(), nil, nil, generated)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 || pending || len(tracker.states) != 0 {
		t.Fatal("explicit Service deletion must remove the model immediately")
	}
}

func TestDiscoveryTrackerStabilizesChangedModelSet(t *testing.T) {
	checker := &fakeHealthChecker{healthy: true, models: []string{"base"}}
	tracker := newDiscoveryTracker(checker, 2, time.Minute)
	services := []corev1.Service{trackedService()}
	slices := []discoveryv1.EndpointSlice{trackedReadySlice()}
	_, _, _ = tracker.Filter(context.Background(), services, slices, "")
	models, _, _ := tracker.Filter(context.Background(), services, slices, "")
	if len(models) != 1 || models[0].Name != "base" {
		t.Fatalf("initial model set was not published: %#v", models)
	}
	checker.models = []string{"base", "lora"}
	models, pending, _ := tracker.Filter(context.Background(), services, slices, "")
	if len(models) != 1 || !pending {
		t.Fatalf("changed set must retain the old set while pending: %#v pending=%v", models, pending)
	}
	models, pending, _ = tracker.Filter(context.Background(), services, slices, "")
	if len(models) != 2 || pending {
		t.Fatalf("stable changed set was not published: %#v pending=%v", models, pending)
	}
}

func TestDiscoveryTrackerMatchesEndpointSlicesByNamespace(t *testing.T) {
	checker := healthCheckerFunc(func(_ context.Context, svc *corev1.Service) ([]string, error) {
		return []string{svc.Namespace + "/model"}, nil
	})
	tracker := newDiscoveryTracker(checker, 1, 0)
	serviceA := trackedService()
	serviceA.Namespace = "team-a"
	serviceB := trackedService()
	serviceB.Namespace = "team-b"
	sliceA := trackedReadySlice()
	sliceA.Namespace = "team-a"

	models, _, err := tracker.Filter(context.Background(), []corev1.Service{serviceA, serviceB}, []discoveryv1.EndpointSlice{sliceA}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].SourceService != "team-a/vllm" {
		t.Fatalf("EndpointSlice from team-a matched a Service in another namespace: %#v", models)
	}

	sliceB := trackedReadySlice()
	sliceB.Namespace = "team-b"
	models, _, err = tracker.Filter(context.Background(), []corev1.Service{serviceA, serviceB}, []discoveryv1.EndpointSlice{sliceA, sliceB}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("expected both namespace-scoped Services, got %#v", models)
	}
}

func TestDiscoveredModelsSupportsFilterAndSingleModelAlias(t *testing.T) {
	svc := trackedService()
	svc.Annotations = map[string]string{
		adapterconfig.FilterAnnotation: "^org/base$",
		adapterconfig.AliasAnnotation:  "production",
	}
	models, err := discoveredModels(&svc, []string{"org/base", "org/lora"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Name != "production" || models[0].UpstreamModel != "org/base" {
		t.Fatalf("unexpected aliased model: %#v", models)
	}

	delete(svc.Annotations, adapterconfig.FilterAnnotation)
	if _, err := discoveredModels(&svc, []string{"org/base", "org/lora"}); err == nil {
		t.Fatal("expected alias with multiple discovered models to fail")
	}
}

func TestHTTPHealthCheckerUsesVLLMModelsAsSourceOfTruth(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := ""
		status := http.StatusOK
		if req.URL.Path == "/v1/models" {
			body = `{"data":[{"id":"org/lora"},{"id":"org/base"},{"id":"org/base"}]}`
		} else if req.URL.Path != "/health" {
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	checker := httpHealthChecker{client: &http.Client{Transport: transport}}
	models, err := checker.Check(context.Background(), ptrService(trackedService()))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "org/base" || models[1] != "org/lora" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func ptrService(svc corev1.Service) *corev1.Service { return &svc }

func trackedService() corev1.Service {
	return corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "vllm", Namespace: "test", Labels: map[string]string{adapterconfig.DiscoveryLabel: "litellm"}}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: adapterconfig.APIPortName, Port: 8000}}}}
}

func trackedReadySlice() discoveryv1.EndpointSlice {
	return discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Labels: map[string]string{discoveryv1.LabelServiceName: "vllm"}}, Endpoints: []discoveryv1.Endpoint{{}}}
}

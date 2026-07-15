package controller

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	adapterconfig "gitlab.sberdevices.ru/rndml/devops/litellm-configurator/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestIntegrationDiscoveryToRollout covers the complete reconcile path with the
// real HTTP health checker and Kubernetes client semantics provided by client-go.
func TestIntegrationDiscoveryToRollout(t *testing.T) {
	ctx := context.Background()
	o := testOptions()
	requests := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		status := http.StatusOK
		body := ""
		switch req.URL.Path {
		case "/health":
		case "/v1/models":
			body = `{"data":[{"id":"org/base"},{"id":"org/lora"}]}`
		default:
			status = http.StatusNotFound
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})
	tracker := newDiscoveryTracker(
		httpHealthChecker{client: &http.Client{Transport: transport, Timeout: time.Second}},
		2,
		2*time.Minute,
	)
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "litellm", Namespace: "test"}},
		baseConfigMap("general_settings:\n  master_key: os.environ/PROXY_MASTER_KEY\n"),
	)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vllm",
			Namespace: "test",
			Labels:    map[string]string{adapterconfig.DiscoveryLabel: "litellm"},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: adapterconfig.APIPortName, Port: 8000}}},
	}
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test", Labels: map[string]string{discoveryv1.LabelServiceName: "vllm"}},
		Endpoints:  []discoveryv1.Endpoint{{}},
	}
	services := serviceList{items: []*corev1.Service{svc}}
	slices := sliceList{items: []*discoveryv1.EndpointSlice{slice}}

	// The first observation creates a valid config but does not publish models.
	if err := reconcileWithTracker(ctx, o, client, services, slices, tracker); err != nil {
		t.Fatal(err)
	}
	cm, err := client.CoreV1().ConfigMaps("test").Get(ctx, "generated", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cm.Data[o.ConfigKey], "model_list: []") {
		t.Fatalf("models were published before the success threshold:\n%s", cm.Data[o.ConfigKey])
	}

	// The second identical observation publishes both the base model and LoRA.
	if err := reconcileWithTracker(ctx, o, client, services, slices, tracker); err != nil {
		t.Fatal(err)
	}
	cm, _ = client.CoreV1().ConfigMaps("test").Get(ctx, "generated", metav1.GetOptions{})
	for _, want := range []string{"model_name: org/base", "model_name: org/lora", "api_base: http://vllm.test.svc.cluster.local:8000/v1"} {
		if !strings.Contains(cm.Data[o.ConfigKey], want) {
			t.Fatalf("generated config does not contain %q:\n%s", want, cm.Data[o.ConfigKey])
		}
	}
	deployment, _ := client.AppsV1().Deployments("test").Get(ctx, "litellm", metav1.GetOptions{})
	if deployment.Spec.Template.Annotations[checksumAnnotation] == "" || !hasSafeRollingUpdate(deployment.Spec.Strategy) {
		t.Fatalf("LiteLLM rollout was not configured: %#v", deployment.Spec)
	}

	// Explicit deletion bypasses the unhealthy grace period.
	if err := reconcileWithTracker(ctx, o, client, serviceList{}, sliceList{}, tracker); err != nil {
		t.Fatal(err)
	}
	cm, _ = client.CoreV1().ConfigMaps("test").Get(ctx, "generated", metav1.GetOptions{})
	if strings.Contains(cm.Data[o.ConfigKey], "org/base") || strings.Contains(cm.Data[o.ConfigKey], "org/lora") {
		t.Fatalf("deleted Service remains in generated config:\n%s", cm.Data[o.ConfigKey])
	}
	if requests != 4 {
		t.Fatalf("expected two health and two models requests, got %d", requests)
	}
}

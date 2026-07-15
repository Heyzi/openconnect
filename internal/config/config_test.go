package config

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateIsDeterministic(t *testing.T) {
	discovered := []DiscoveredModel{
		{Name: "z-model", UpstreamModel: "upstream-z", APIBase: "http://z-service.test.svc.cluster.local:8000/v1", SourceService: "test/z-service"},
		{Name: "a-model", UpstreamModel: "upstream-a", APIBase: "http://a-service.test.svc.cluster.local:9000/v1", SourceService: "test/a-service"},
	}
	data, checksum, err := Generate([]byte("general_settings:\n  master_key: os.environ/PROXY_MASTER_KEY\n"), discovered)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Index(got, "a-model") > strings.Index(got, "z-model") {
		t.Fatalf("models are not sorted:\n%s", got)
	}
	if !strings.Contains(got, "http://a-service.test.svc.cluster.local:9000/v1") {
		t.Fatalf("unexpected api_base:\n%s", got)
	}
	if !strings.Contains(got, "model: openai/upstream-a") {
		t.Fatalf("upstream vLLM model id was not preserved:\n%s", got)
	}
	if !strings.Contains(got, "master_key: os.environ/PROXY_MASTER_KEY") {
		t.Fatalf("base config was not preserved:\n%s", got)
	}
	if len(checksum) != 64 {
		t.Fatalf("invalid checksum %q", checksum)
	}
}

func TestGenerateRejectsDuplicateModel(t *testing.T) {
	discovered := []DiscoveredModel{
		{Name: "model", UpstreamModel: "one", APIBase: "http://one/v1", SourceService: "test/one"},
		{Name: "model", UpstreamModel: "two", APIBase: "http://two/v1", SourceService: "test/two"},
	}
	_, _, err := Generate(nil, discovered)
	if err == nil || !strings.Contains(err.Error(), "duplicate model") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestGenerateEmptyConfig(t *testing.T) {
	data, _, err := Generate([]byte("litellm_settings:\n  cache: true\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "litellm_settings:\n  cache: true\nmodel_list: []\n" {
		t.Fatalf("unexpected empty config %q", data)
	}
}

func TestGenerateRejectsModelListInBase(t *testing.T) {
	_, _, err := Generate([]byte("model_list: []\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "must not contain model_list") {
		t.Fatalf("expected ownership error, got %v", err)
	}
}

func TestGenerateRejectsInvalidBaseYAML(t *testing.T) {
	_, _, err := Generate([]byte("settings: [\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "parse base config") {
		t.Fatalf("expected YAML error, got %v", err)
	}
}

func TestServiceEndpointRequiresNamedVLLMPort(t *testing.T) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "vllm", Namespace: "test"}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "metrics", Port: 9090}, {Name: APIPortName, Port: 8000}}}}
	endpoint, err := ServiceEndpoint(svc)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://vllm.test.svc.cluster.local:8000" {
		t.Fatalf("unexpected endpoint %q", endpoint)
	}
	svc.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: 8000}}
	if _, err := ServiceEndpoint(svc); err == nil || !strings.Contains(err.Error(), APIPortName) {
		t.Fatalf("expected missing named port error, got %v", err)
	}
}

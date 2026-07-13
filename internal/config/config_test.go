package config

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateIncludesOnlyReadyAndIsDeterministic(t *testing.T) {
	services := []corev1.Service{
		svc("z-service", "z-model", "1", 8000),
		svc("a-service", "a-model", "2", 9000),
		svc("not-ready", "hidden", "1", 7000),
	}
	yes := true
	slices := []discoveryv1.EndpointSlice{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{discoveryv1.LabelServiceName: "z-service"}}, Endpoints: []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &yes}}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{discoveryv1.LabelServiceName: "a-service"}}, Endpoints: []discoveryv1.Endpoint{{}}},
	}
	data, checksum, err := Generate([]byte("general_settings:\n  master_key: os.environ/PROXY_MASTER_KEY\n"), services, slices)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "hidden") {
		t.Fatalf("unready model was published:\n%s", got)
	}
	if strings.Index(got, "a-model") > strings.Index(got, "z-model") {
		t.Fatalf("models are not sorted:\n%s", got)
	}
	if !strings.Contains(got, "http://a-service.test.svc.cluster.local:9000/v1") {
		t.Fatalf("unexpected api_base:\n%s", got)
	}
	if !strings.Contains(got, "master_key: os.environ/PROXY_MASTER_KEY") {
		t.Fatalf("base config was not preserved:\n%s", got)
	}
	if len(checksum) != 64 {
		t.Fatalf("invalid checksum %q", checksum)
	}
}

func TestGenerateRejectsDuplicateModel(t *testing.T) {
	services := []corev1.Service{svc("one", "model", "1", 8000), svc("two", "model", "2", 8000)}
	slices := []discoveryv1.EndpointSlice{readySlice("one"), readySlice("two")}
	_, _, err := Generate(nil, services, slices)
	if err == nil || !strings.Contains(err.Error(), "duplicate model") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestGenerateEmptyConfig(t *testing.T) {
	data, _, err := Generate([]byte("litellm_settings:\n  cache: true\n"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "litellm_settings:\n  cache: true\nmodel_list: []\n" {
		t.Fatalf("unexpected empty config %q", data)
	}
}

func TestGenerateRejectsModelListInBase(t *testing.T) {
	_, _, err := Generate([]byte("model_list: []\n"), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "must not contain model_list") {
		t.Fatalf("expected ownership error, got %v", err)
	}
}

func TestGenerateRejectsInvalidBaseYAML(t *testing.T) {
	_, _, err := Generate([]byte("settings: [\n"), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "parse base config") {
		t.Fatalf("expected YAML error, got %v", err)
	}
}

func svc(serviceName, modelName, version string, port int32) corev1.Service {
	return corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: "test", Labels: map[string]string{DiscoveryLabel: "litellm", NameLabel: modelName, VersionLabel: version}}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: port}}}}
}
func readySlice(serviceName string) discoveryv1.EndpointSlice {
	return discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{discoveryv1.LabelServiceName: serviceName}}, Endpoints: []discoveryv1.Endpoint{{}}}
}

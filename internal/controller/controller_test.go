package controller

import (
	"context"
	"strings"
	"testing"

	adapterconfig "gitlab.sberdevices.ru/rndml/devops/litellm-configurator/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"
)

type serviceList struct{ items []*corev1.Service }

func (l serviceList) List(labels.Selector) ([]*corev1.Service, error) { return l.items, nil }

type sliceList struct{ items []*discoveryv1.EndpointSlice }

func (l sliceList) List(labels.Selector) ([]*discoveryv1.EndpointSlice, error) { return l.items, nil }

func TestReconcileAddsAndRemovesModelAndRollsDeployment(t *testing.T) {
	ctx := context.Background()
	o := testOptions()
	client := fake.NewSimpleClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "litellm", Namespace: "test"}}, baseConfigMap("litellm_settings:\n  cache: true\n"))
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "vllm", Namespace: "test", Labels: map[string]string{
			adapterconfig.DiscoveryLabel: "litellm", adapterconfig.NameLabel: "llama", adapterconfig.VersionLabel: "1",
		}},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8000}}},
	}
	slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{discoveryv1.LabelServiceName: "vllm"}}, Endpoints: []discoveryv1.Endpoint{{}}}
	if err := reconcile(ctx, o, client, serviceList{[]*corev1.Service{svc}}, sliceList{[]*discoveryv1.EndpointSlice{slice}}); err != nil {
		t.Fatal(err)
	}
	cm, _ := client.CoreV1().ConfigMaps("test").Get(ctx, "generated", metav1.GetOptions{})
	if !strings.Contains(cm.Data[o.ConfigKey], "model_name: llama") {
		t.Fatalf("model not generated:\n%s", cm.Data[o.ConfigKey])
	}
	deployment, _ := client.AppsV1().Deployments("test").Get(ctx, "litellm", metav1.GetOptions{})
	firstChecksum := deployment.Spec.Template.Annotations[checksumAnnotation]
	if firstChecksum == "" {
		t.Fatal("rollout checksum was not patched")
	}

	if err := reconcile(ctx, o, client, serviceList{}, sliceList{}); err != nil {
		t.Fatal(err)
	}
	cm, _ = client.CoreV1().ConfigMaps("test").Get(ctx, "generated", metav1.GetOptions{})
	if cm.Data[o.ConfigKey] != "litellm_settings:\n  cache: true\nmodel_list: []\n" {
		t.Fatalf("deleted model remains:\n%s", cm.Data[o.ConfigKey])
	}
	deployment, _ = client.AppsV1().Deployments("test").Get(ctx, "litellm", metav1.GetOptions{})
	if deployment.Spec.Template.Annotations[checksumAnnotation] == firstChecksum {
		t.Fatal("checksum did not change after model deletion")
	}
}

func TestReconcileKeepsLastConfigOnDuplicate(t *testing.T) {
	ctx := context.Background()
	o := testOptions()
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "litellm", Namespace: "test"}},
		baseConfigMap("litellm_settings:\n  cache: true\n"),
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "generated", Namespace: "test"}, Data: map[string]string{"config.yaml": "last-valid"}},
	)
	makeService := func(name string) *corev1.Service {
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test", Labels: map[string]string{adapterconfig.DiscoveryLabel: "litellm", adapterconfig.NameLabel: "same", adapterconfig.VersionLabel: "1"}}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8000}}}}
	}
	makeSlice := func(name string) *discoveryv1.EndpointSlice {
		return &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{discoveryv1.LabelServiceName: name}}, Endpoints: []discoveryv1.Endpoint{{}}}
	}
	err := reconcile(ctx, o, client, serviceList{[]*corev1.Service{makeService("one"), makeService("two")}}, sliceList{[]*discoveryv1.EndpointSlice{makeSlice("one"), makeSlice("two")}})
	if err == nil || !strings.Contains(err.Error(), "duplicate model") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	cm, _ := client.CoreV1().ConfigMaps("test").Get(ctx, "generated", metav1.GetOptions{})
	if cm.Data[o.ConfigKey] != "last-valid" {
		t.Fatalf("last valid config was overwritten: %q", cm.Data[o.ConfigKey])
	}
}

func TestReconcileKeepsLastConfigWhenBaseIsInvalid(t *testing.T) {
	ctx := context.Background()
	o := testOptions()
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "litellm", Namespace: "test"}},
		baseConfigMap("model_list: []\n"),
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "generated", Namespace: "test"}, Data: map[string]string{"config.yaml": "last-valid"}},
	)
	err := reconcile(ctx, o, client, serviceList{}, sliceList{})
	if err == nil || !strings.Contains(err.Error(), "must not contain model_list") {
		t.Fatalf("expected base config error, got %v", err)
	}
	cm, _ := client.CoreV1().ConfigMaps("test").Get(ctx, "generated", metav1.GetOptions{})
	if cm.Data[o.ConfigKey] != "last-valid" {
		t.Fatalf("last valid config was overwritten: %q", cm.Data[o.ConfigKey])
	}
}

func testOptions() Options {
	return Options{Namespace: "test", BaseConfigMapName: "base", BaseConfigKey: "config.yaml", ConfigMapName: "generated", ConfigKey: "config.yaml", DeploymentName: "litellm"}
}
func baseConfigMap(data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "base", Namespace: "test"}, Data: map[string]string{"config.yaml": data}}
}

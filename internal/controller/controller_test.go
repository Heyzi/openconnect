package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	adapterconfig "gitlab.sberdevices.ru/rndml/devops/litellm-configurator/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
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
			adapterconfig.DiscoveryLabel: "litellm",
		}},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: adapterconfig.APIPortName, Port: 8000}}},
	}
	slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Labels: map[string]string{discoveryv1.LabelServiceName: "vllm"}}, Endpoints: []discoveryv1.Endpoint{{}}}
	tracker := newDiscoveryTracker(&fakeHealthChecker{healthy: true, models: []string{"llama"}}, 1, 2*time.Minute)
	if err := reconcileWithTracker(ctx, o, client, serviceList{[]*corev1.Service{svc}}, sliceList{[]*discoveryv1.EndpointSlice{slice}}, tracker); err != nil {
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
	if !hasSafeRollingUpdate(deployment.Spec.Strategy) {
		t.Fatalf("safe RollingUpdate strategy was not configured: %#v", deployment.Spec.Strategy)
	}

	if err := reconcileWithTracker(ctx, o, client, serviceList{}, sliceList{}, tracker); err != nil {
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
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test", Labels: map[string]string{adapterconfig.DiscoveryLabel: "litellm"}}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: adapterconfig.APIPortName, Port: 8000}}}}
	}
	makeSlice := func(name string) *discoveryv1.EndpointSlice {
		return &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Labels: map[string]string{discoveryv1.LabelServiceName: name}}, Endpoints: []discoveryv1.Endpoint{{}}}
	}
	tracker := newDiscoveryTracker(&fakeHealthChecker{healthy: true, models: []string{"same"}}, 1, 2*time.Minute)
	err := reconcileWithTracker(ctx, o, client, serviceList{[]*corev1.Service{makeService("one"), makeService("two")}}, sliceList{[]*discoveryv1.EndpointSlice{makeSlice("one"), makeSlice("two")}}, tracker)
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

func TestReconcileDoesNotPatchDeploymentWhenChecksumIsAlreadyLoaded(t *testing.T) {
	ctx := context.Background()
	o := testOptions()
	data, checksum, err := adapterconfig.Generate([]byte("litellm_settings:\n  cache: true\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "litellm", Namespace: "test"},
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{checksumAnnotation: checksum}}},
				Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType, RollingUpdate: &appsv1.RollingUpdateDeployment{MaxUnavailable: intOrString(0), MaxSurge: intOrString(1)}},
			},
		},
		baseConfigMap("litellm_settings:\n  cache: true\n"),
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "generated", Namespace: "test"}, Data: map[string]string{"config.yaml": string(data)}},
	)
	patches := 0
	client.PrependReactor("patch", "deployments", func(action ktesting.Action) (bool, runtime.Object, error) {
		patches++
		return false, nil, nil
	})
	if err := reconcile(ctx, o, client, serviceList{}, sliceList{}); err != nil {
		t.Fatal(err)
	}
	if patches != 0 {
		t.Fatalf("deployment was patched %d times although the checksum was already loaded", patches)
	}
}

func intOrString(value int) *intstr.IntOrString {
	v := intstr.FromInt(value)
	return &v
}

func testOptions() Options {
	return Options{Namespace: "test", BaseConfigMapName: "base", BaseConfigKey: "config.yaml", ConfigMapName: "generated", ConfigKey: "config.yaml", DeploymentName: "litellm"}
}
func baseConfigMap(data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "base", Namespace: "test"}, Data: map[string]string{"config.yaml": data}}
}

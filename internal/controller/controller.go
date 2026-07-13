package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	adapterconfig "gitlab.sberdevices.ru/rndml/devops/litellm-configurator/internal/config"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const checksumAnnotation = "models.sberdevices.ru/litellm-config-checksum"

type Options struct {
	Namespace, BaseConfigMapName, BaseConfigKey, ConfigMapName, DeploymentName, ConfigKey string
}

func Run(o Options, client kubernetes.Interface) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	factory := informers.NewSharedInformerFactoryWithOptions(client, 10*time.Minute, informers.WithNamespace(o.Namespace))
	services := factory.Core().V1().Services()
	configMaps := factory.Core().V1().ConfigMaps()
	slices := factory.Discovery().V1().EndpointSlices()
	queue := make(chan struct{}, 1)
	enqueue := func(_ interface{}) {
		select {
		case queue <- struct{}{}:
		default:
		}
	}
	_, _ = services.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: enqueue, UpdateFunc: func(_, n interface{}) { enqueue(n) }, DeleteFunc: enqueue})
	_, _ = configMaps.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: enqueue, UpdateFunc: func(_, n interface{}) { enqueue(n) }, DeleteFunc: enqueue})
	_, _ = slices.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: enqueue, UpdateFunc: func(_, n interface{}) { enqueue(n) }, DeleteFunc: enqueue})
	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), services.Informer().HasSynced, configMaps.Informer().HasSynced, slices.Informer().HasSynced) {
		return fmt.Errorf("cache sync failed")
	}
	enqueue(nil)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			enqueue(nil)
		case <-queue:
			// Coalesce bursts caused by a Helm release creating multiple objects.
			time.Sleep(300 * time.Millisecond)
			err := wait.ExponentialBackoffWithContext(ctx, wait.Backoff{Duration: time.Second, Factor: 2, Steps: 5, Cap: 15 * time.Second}, func(ctx context.Context) (bool, error) {
				if err := reconcile(ctx, o, client, services.Lister(), slices.Lister()); err != nil {
					slog.Error("reconcile failed", "error", err)
					return false, nil
				}
				return true, nil
			})
			if err != nil && ctx.Err() == nil {
				slog.Error("reconcile retries exhausted", "error", err)
			}
		}
	}
}

func reconcile(ctx context.Context, o Options, client kubernetes.Interface, serviceLister interface {
	List(selector labels.Selector) ([]*corev1.Service, error)
}, sliceLister interface {
	List(selector labels.Selector) ([]*discoveryv1.EndpointSlice, error)
}) error {
	base, err := client.CoreV1().ConfigMaps(o.Namespace).Get(ctx, o.BaseConfigMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read base ConfigMap %s/%s: %w", o.Namespace, o.BaseConfigMapName, err)
	}
	baseData, exists := base.Data[o.BaseConfigKey]
	if !exists {
		return fmt.Errorf("base ConfigMap %s/%s does not contain key %q", o.Namespace, o.BaseConfigMapName, o.BaseConfigKey)
	}
	servicePtrs, err := serviceLister.List(labels.SelectorFromSet(labels.Set{adapterconfig.DiscoveryLabel: "litellm"}))
	if err != nil {
		return fmt.Errorf("list Services: %w", err)
	}
	slicePtrs, err := sliceLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("list EndpointSlices: %w", err)
	}
	services := make([]corev1.Service, len(servicePtrs))
	for i := range servicePtrs {
		services[i] = *servicePtrs[i]
	}
	slices := make([]discoveryv1.EndpointSlice, len(slicePtrs))
	for i := range slicePtrs {
		slices[i] = *slicePtrs[i]
	}
	data, checksum, err := adapterconfig.Generate([]byte(baseData), services, slices)
	if err != nil {
		return fmt.Errorf("validate discovered models (last valid config retained): %w", err)
	}
	cmClient := client.CoreV1().ConfigMaps(o.Namespace)
	cm, err := cmClient.Get(ctx, o.ConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = cmClient.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: o.ConfigMapName, Namespace: o.Namespace}, Data: map[string]string{o.ConfigKey: string(data)}}, metav1.CreateOptions{})
	} else if err == nil {
		if cm.Data[o.ConfigKey] != string(data) {
			cm = cm.DeepCopy()
			if cm.Data == nil {
				cm.Data = map[string]string{}
			}
			cm.Data[o.ConfigKey] = string(data)
			_, err = cmClient.Update(ctx, cm, metav1.UpdateOptions{})
		}
	}
	if err != nil {
		return fmt.Errorf("write ConfigMap: %w", err)
	}
	patch := []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"%s":"%s"}}}}}`, checksumAnnotation, checksum))
	if _, err := client.AppsV1().Deployments(o.Namespace).Patch(ctx, o.DeploymentName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("patch LiteLLM Deployment: %w", err)
	}
	slog.Info("LiteLLM config reconciled", "checksum", checksum, "services", len(services))
	return nil
}

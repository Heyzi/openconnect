package main

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"gitlab.sberdevices.ru/rndml/devops/litellm-configurator/internal/controller"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	var o controller.Options
	flag.StringVar(&o.Namespace, "namespace", env("POD_NAMESPACE", "default"), "namespace containing adapter config and LiteLLM Deployment")
	flag.BoolVar(&o.ClusterWideDiscovery, "cluster-wide-discovery", false, "discover vLLM Services in all namespaces")
	flag.StringVar(&o.BaseConfigMapName, "base-configmap", "litellm-base-config", "user-managed base ConfigMap name")
	flag.StringVar(&o.BaseConfigKey, "base-config-key", "config.yaml", "base ConfigMap data key")
	flag.StringVar(&o.ConfigMapName, "configmap", "litellm-generated-config", "generated ConfigMap name")
	flag.StringVar(&o.DeploymentName, "deployment", "litellm", "LiteLLM Deployment name")
	flag.StringVar(&o.ConfigKey, "config-key", "config.yaml", "ConfigMap data key")
	flag.DurationVar(&o.HealthTimeout, "health-timeout", 5*time.Second, "timeout for each vLLM health request")
	flag.IntVar(&o.HealthSuccessThreshold, "health-success-threshold", 2, "consecutive successful checks required before publishing a model")
	flag.DurationVar(&o.UnhealthyGracePeriod, "unhealthy-grace-period", 2*time.Minute, "how long to retain a published model while vLLM is temporarily unhealthy")
	flag.Parse()
	if o.HealthSuccessThreshold < 1 || o.HealthTimeout <= 0 || o.UnhealthyGracePeriod < 0 {
		slog.Error("invalid health check settings")
		os.Exit(2)
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		slog.Error("create in-cluster config", "error", err)
		os.Exit(1)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		slog.Error("create kubernetes client", "error", err)
		os.Exit(1)
	}
	if err := controller.Run(o, client); err != nil {
		slog.Error("controller stopped", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

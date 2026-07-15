# Подключение существующего vLLM к LiteLLM

## 1. Включить discovery для vLLM Service

У существующего vLLM Service добавьте label и именованный API-порт:

```yaml
metadata:
  labels:
    models.sberdevices.ru/discovery: litellm
spec:
  ports:
    - name: vllm-http
      port: 8000
      targetPort: 8000
```

Адаптер использует Service DNS и проверяет:

- `GET /health`;
- `GET /v1/models`.

Если Service работает через HTTPS:

```yaml
metadata:
  annotations:
    models.sberdevices.ru/scheme: https
```

Необязательные настройки:

```yaml
metadata:
  annotations:
    # Оставить только подходящие ID из /v1/models.
    models.sberdevices.ru/model-filter: "^meta-llama/"
    # Публичное имя; разрешено только после фильтрации до одной модели.
    models.sberdevices.ru/alias: llama-production
```

## 2. Установить config adapter

Минимальный `adapter-values.yaml`:

```yaml
image:
  repository: registry.example.com/litellm-config-adapter
  tag: "0.1.0"

# Для закрытого registry.
imagePullSecrets:
  - name: registry-pull-secret

litellm:
  deploymentName: litellm

baseConfig:
  general_settings:
    master_key: os.environ/PROXY_MASTER_KEY
  litellm_settings:
    cache: true
    success_callback:
      - prometheus
```

```bash
helm upgrade --install litellm-config-adapter \
  ./charts/litellm-config-adapter \
  --namespace litellm \
  -f adapter-values.yaml
```

Адаптер работает в том же namespace, где находятся vLLM Services и LiteLLM
Deployment.

## 3. Подключить generated ConfigMap к LiteLLM

Создайте secrets любым принятым способом. Chart ожидает:

- `litellm-env-secret`, ключ `PROXY_MASTER_KEY`;
- `litellm-redis`, ключ `redis-password`.

Минимальный качественный `litellm-values.yaml` для официального chart `1.92.0`:

```yaml
fullnameOverride: litellm

replicaCount: 2

proxyConfigMap:
  create: false
  name: litellm-generated-config
  key: config.yaml

masterkeySecretName: litellm-env-secret
masterkeySecretKey: PROXY_MASTER_KEY

strategy:
  type: RollingUpdate
  rollingUpdate:
    maxUnavailable: 0
    maxSurge: 1

# После SIGTERM Kubernetes даёт LiteLLM до 30 минут на завершение
# активных запросов перед принудительной остановкой.
terminationGracePeriodSeconds: 1800

pdb:
  enabled: true
  minAvailable: 1

resources:
  requests:
    cpu: 250m
    memory: 512Mi
  limits:
    cpu: "1"
    memory: 1Gi

serviceMonitor:
  enabled: true
  interval: 15s
  scrapeTimeout: 10s

# Статическая конфигурация из ConfigMap не требует PostgreSQL.
db:
  useExisting: false
  deployStandalone: false

migrationJob:
  enabled: false

redis:
  enabled: true
  architecture: standalone
  auth:
    enabled: true
    existingSecret: litellm-redis
    existingSecretPasswordKey: redis-password
  master:
    persistence:
      enabled: true
      size: 2Gi
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
      limits:
        cpu: 500m
        memory: 512Mi
```

```bash
helm upgrade --install litellm \
  oci://ghcr.io/berriai/litellm-helm \
  --version 1.92.0 \
  --namespace litellm \
  -f litellm-values.yaml
```

`baseConfig.litellm_settings.cache: true` включает Redis, а
`success_callback: [prometheus]` — метрики LiteLLM на `/metrics/`.
`serviceMonitor.enabled: true` добавляет scrape этого endpoint через Prometheus
Operator. При необходимости добавьте в `serviceMonitor.labels` labels, которые
выбирает ваш Prometheus. Если в кластере нет default StorageClass, укажите
`redis.master.persistence.storageClass`.

Имя LiteLLM Deployment должно совпадать с
`litellm.deploymentName` в values адаптера.

## 4. Настроить CI deploy

Pipeline собирает и публикует image, затем на default branch выполняет
`helm upgrade --install` адаптера.

В GitLab CI/CD variables задайте:

- `KUBE_CONTEXT` — context GitLab Kubernetes Agent;
- `DEPLOY_NAMESPACE` — namespace, по умолчанию `litellm`;
- `ADAPTER_VALUES_FILE` — file variable с содержимым `adapter-values.yaml`.

`image.repository` и `image.tag` pipeline заменяет на GitLab Registry и SHA
текущего commit.

## 5. Проверить

```bash
kubectl -n litellm logs deployment/litellm-config-adapter --tail=100
kubectl -n litellm get configmap litellm-generated-config \
  -o jsonpath='{.data.config\.yaml}'
kubectl -n litellm rollout status deployment/litellm
```

Если модели нет, проверьте label Service, имя порта `vllm-http`, готовый
EndpointSlice и ответы `/health` и `/v1/models`.

## 6. Cluster-wide discovery

Чтобы держать LiteLLM и адаптер в namespace `litellm`, а vLLM — в любых других
namespace, включите:

```yaml
discovery:
  clusterWide: true
```

Chart создаст `ClusterRole` только с `get/list/watch` для Services и
EndpointSlices. Запись ConfigMap и изменение LiteLLM Deployment останутся
ограничены namespace `litellm` через обычный `Role`.

На vLLM Services в других namespace нужны те же label и порт `vllm-http` из
первого шага. Также разрешите NetworkPolicy egress из namespace `litellm` к
портам vLLM Services.

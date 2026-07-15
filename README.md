# litellm-config-adapter

Контроллер объединяет ручной `ConfigMap/litellm-base-config` с автоматически
обнаруженными vLLM моделями, записывает результат в
`ConfigMap/litellm-generated-config` и меняет checksum-аннотацию pod template
LiteLLM `Deployment`. Изменение base config или списка моделей запускает rollout.

Адаптер намеренно работает в одной реплике: это простой single-writer controller
без leader election. Helm chart фиксирует `replicaCount: 1` и использует
`Recreate`, чтобы при обновлении две версии адаптера не работали одновременно.

## Применение конфигурации

Официальный LiteLLM chart монтирует `config.yaml` через `subPath`, поэтому уже
запущенный контейнер не видит обновление ConfigMap. Сам LiteLLM загружает
статический YAML при старте и не следит за изменением файла. По этой причине
адаптер применяет новую конфигурацию через идемпотентный rollout: сначала
записывает ConfigMap, затем меняет checksum pod template только если Deployment
ещё не использует этот checksum.

Для rollout без потери доступности настройте официальный LiteLLM chart:

```yaml
strategy:
  type: RollingUpdate
  rollingUpdate:
    maxUnavailable: 0
    maxSurge: 1

pdb:
  enabled: true
  minAvailable: 1

terminationGracePeriodSeconds: 90

environmentSecrets:
  - litellm-env-secret # содержит DRAIN_ENDPOINT_TOKEN

lifecycle:
  preStop:
    exec:
      command:
        - python
        - -c
        - |
          import os, urllib.request
          request = urllib.request.Request("http://127.0.0.1:4000/health/drain")
          request.add_header("X-Drain-Token", os.environ["DRAIN_ENDPOINT_TOKEN"])
          urllib.request.urlopen(request, timeout=35).read()
```

Адаптер сам закрепляет `RollingUpdate` с `maxUnavailable: 0` и `maxSurge: 1` при
обновлении checksum. PDB и lifecycle принадлежат официальному LiteLLM chart.
Для drain добавьте в `baseConfig`:

```yaml
general_settings:
  enable_drain_endpoint: true
  drain_endpoint_token: os.environ/DRAIN_ENDPOINT_TOKEN
```

Динамическое обновление моделей без рестарта в LiteLLM возможно только в другом
режиме управления: с PostgreSQL, `store_model_in_db: true` и API
`/model/new`, `/model/update`, `/model/delete`. LiteLLM синхронизирует такие
модели между процессами из БД. Адаптер намеренно не смешивает DB-managed модели
со статическим YAML: у этих режимов должны быть разные владельцы конфигурации.

## Разделение конфигурации

Все ручные настройки задаются в base ConfigMap. Поле `model_list` там запрещено,
поскольку им эксклюзивно управляет адаптер:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: litellm-base-config
data:
  config.yaml: |
    general_settings:
      master_key: os.environ/PROXY_MASTER_KEY
    litellm_settings:
      cache: true
      success_callback:
        - prometheus
```

Итоговый `litellm-generated-config` вручную редактировать не нужно: при reconcile
он будет пересобран. При некорректном YAML, ручном `model_list` или дубликате
моделей последний валидный generated config сохраняется.

## Контракт discovery

Обязателен только opt-in label на `Service`:

```yaml
metadata:
  labels:
    models.sberdevices.ru/discovery: litellm
```

Модель публикуется после двух последовательных успешных проверок: у Service есть
готовый EndpointSlice, `/health` отвечает 2xx, а `/v1/models` возвращает
непустой список. Каждый уникальный `data[].id`, включая LoRA adapters,
публикуется как отдельная модель LiteLLM. Временно недоступный опубликованный
набор сохраняется в конфигурации две минуты; удаление Service или discovery
label удаляет его сразу. Параметры настраиваются через `healthChecks` values.

По умолчанию discovery ограничен namespace адаптера. Для чтения vLLM Services
из всех namespace включите `discovery.clusterWide: true`; права записи при этом
останутся ограничены namespace адаптера.

Одинаковый model ID у двух Service делает reconcile невалидным: контроллер пишет
ошибку и сохраняет последний корректный config. Необязательные annotations:

```yaml
metadata:
  annotations:
    # Публичное имя; допустимо только если после фильтра остаётся одна модель.
    models.sberdevices.ru/alias: llama-production
    # RE2-фильтр для data[].id из /v1/models.
    models.sberdevices.ru/model-filter: "^meta-llama/"
```

API-порт Service обязан называться `vllm-http`; порядок портов значения не имеет:

```yaml
spec:
  ports:
    - name: vllm-http
      port: 8000
      targetPort: 8000
```

По умолчанию используется HTTP. Для HTTPS добавьте annotation
`models.sberdevices.ru/scheme: https`.

Для `vllm-model` chart metadata следует добавить в шаблон Service под opt-in value:

```yaml
labels:
  {{- with .Values.litellmDiscovery }}
  {{- if .enabled }}
  models.sberdevices.ru/discovery: litellm
  {{- end }}
  {{- end }}
annotations:
  {{- with .Values.litellmDiscovery }}
  {{- if .enabled }}
  {{- with .modelFilter }}
  models.sberdevices.ru/model-filter: {{ . | quote }}
  {{- end }}
  {{- with .alias }}
  models.sberdevices.ru/alias: {{ . | quote }}
  {{- end }}
  {{- end }}
  {{- end }}
```

## Установка

Пошаговый рецепт развёртывания всего стенда находится в
[`DEPLOYMENT_RECIPE.md`](DEPLOYMENT_RECIPE.md).

Сначала установите адаптер (предварительно опубликовав image и задав repository/tag):

```bash
helm upgrade --install litellm-config-adapter ./charts/litellm-config-adapter \
  --namespace litellm --create-namespace \
  --set image.repository=registry.example.com/litellm-config-adapter \
  --set image.tag=0.1.0 \
  --set litellm.deploymentName=litellm
```

Ручные настройки можно передать через `baseConfig` values. Если base ConfigMap
создаётся отдельно через GitOps, задайте:

```yaml
baseConfigMap:
  create: false
  name: litellm-base-config
  key: config.yaml
```

Затем официальный LiteLLM chart с external ConfigMap. Значения для конкретной
версии chart нужно сверять через `helm show values`:

```yaml
# litellm-values.yaml
proxyConfigMap:
  create: false
  name: litellm-generated-config
  key: config.yaml

redis:
  enabled: true
```

```bash
helm upgrade --install litellm oci://ghcr.io/berriai/litellm-helm \
  --namespace litellm -f litellm-values.yaml
```

Имена namespace и LiteLLM Deployment должны совпадать с настройками адаптера.
RBAC намеренно ограничен одним namespace и указанным Deployment.

## Разработка

```bash
go test ./...
go test -race ./...
go vet ./...
docker build -t litellm-config-adapter:dev .
helm lint charts/litellm-config-adapter
```

GitLab CI выполняет эти проверки, собирает бинарник и Helm package. Container
image публикуется в GitLab Container Registry с тегом commit SHA; Git tag также
публикуется как image tag и `latest`. После успешной сборки default branch job
`deploy-adapter` обновляет Helm release в Kubernetes с образом текущего commit.
Необходимые CI/CD variables описаны в пошаговом рецепте.

Генерируемая запись имеет вид:

```yaml
litellm_settings:
  cache: true
  success_callback:
    - prometheus
general_settings:
  master_key: os.environ/PROXY_MASTER_KEY
model_list:
  - model_name: llama-3.1-8b
    litellm_params:
      model: openai/llama-3.1-8b
      api_base: http://vllm.namespace.svc.cluster.local:8000/v1
      api_key: EMPTY
    model_info:
      source_service: namespace/vllm
```

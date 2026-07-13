# litellm-config-adapter

Контроллер объединяет ручной `ConfigMap/litellm-base-config` с автоматически
обнаруженными vLLM моделями, записывает результат в
`ConfigMap/litellm-generated-config` и меняет checksum-аннотацию pod template
LiteLLM `Deployment`. Изменение base config или списка моделей запускает rollout.

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
```

Итоговый `litellm-generated-config` вручную редактировать не нужно: при reconcile
он будет пересобран. При некорректном YAML, ручном `model_list` или дубликате
моделей последний валидный generated config сохраняется.

## Контракт discovery

Opt-in labels на `Service` обязательны:

```yaml
metadata:
  labels:
    models.sberdevices.ru/discovery: litellm
    models.sberdevices.ru/name: llama-3.1-8b
    models.sberdevices.ru/version: "1"
```

Модель публикуется только если у Service есть EndpointSlice хотя бы с одним
готовым endpoint. Одинаковые `name` у двух готовых Service делают reconcile
невалидным: контроллер пишет ошибку и сохраняет последний корректный config.

По умолчанию используется первый `spec.ports` и HTTP. Это можно переопределить:

```yaml
metadata:
  annotations:
    models.sberdevices.ru/port: http   # имя порта или номер
    models.sberdevices.ru/scheme: http # http или https
```

Для `vllm-model` chart labels следует добавить в шаблон Service под opt-in value:

```yaml
{{- with .Values.litellmDiscovery }}
{{- if .enabled }}
models.sberdevices.ru/discovery: litellm
models.sberdevices.ru/name: {{ $.Values.model.servedName | quote }}
models.sberdevices.ru/version: {{ $.Values.model.version | quote }}
{{- end }}
{{- end }}
```

## Установка

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
docker build -t litellm-config-adapter:dev .
helm lint charts/litellm-config-adapter
```

Генерируемая запись имеет вид:

```yaml
litellm_settings:
  cache: true
general_settings:
  master_key: os.environ/PROXY_MASTER_KEY
model_list:
  - model_name: llama-3.1-8b
    litellm_params:
      model: openai/llama-3.1-8b
      api_base: http://vllm.namespace.svc.cluster.local:8000/v1
      api_key: EMPTY
    model_info:
      version: "1"
```

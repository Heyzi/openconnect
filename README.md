# litellm-config-adapter

Kubernetes-контроллер собирает конфигурацию LiteLLM из двух источников:

- пользовательских настроек в `ConfigMap/litellm-base-config`;
- моделей vLLM, найденных по Kubernetes Service.

Результат записывается в `ConfigMap/litellm-generated-config`. При изменении
конфигурации контроллер обновляет checksum-аннотацию LiteLLM Deployment и
запускает безопасный rolling update (`maxUnavailable: 0`, `maxSurge: 1`).

Контроллер рассчитан на одну реплику и не использует leader election.

## Discovery

Добавьте opt-in label и именованный API-порт в Service каждого vLLM:

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

Модель публикуется, когда Service имеет готовый EndpointSlice, `/health`
отвечает 2xx, а `/v1/models` возвращает непустой список. Все уникальные
`data[].id`, включая LoRA adapters, становятся отдельными моделями LiteLLM.

Поддерживаемые annotations:

```yaml
metadata:
  annotations:
    # RE2-фильтр для data[].id
    models.sberdevices.ru/model-filter: "^meta-llama/"
    # Публичное имя; после фильтра должна остаться ровно одна модель
    models.sberdevices.ru/alias: llama-production
    # Необязательно, по умолчанию http
    models.sberdevices.ru/scheme: https
```

Новый набор моделей публикуется после заданного числа успешных проверок.
Временно недоступные опубликованные модели сохраняются на время grace period.
Удаление Service или discovery label удаляет модели сразу. Дубликат публичного
имени делает reconcile невалидным, при этом последний корректный config
сохраняется.

### Все namespaces

По умолчанию контроллер ищет vLLM только в своем namespace. Для discovery по
всему кластеру включите:

```yaml
discovery:
  clusterWide: true
```

Chart создаст ClusterRole только для чтения Services и EndpointSlices. Доступ к
ConfigMap и LiteLLM Deployment останется ограничен namespace релиза. Убедитесь,
что NetworkPolicy разрешает адаптеру обращаться к vLLM в других namespaces.

## Конфигурация LiteLLM

Ручные настройки задаются в `baseConfig`. Поле `model_list` запрещено: им
полностью управляет контроллер.

```yaml
baseConfig:
  general_settings:
    master_key: os.environ/PROXY_MASTER_KEY
  litellm_settings:
    cache: true
    success_callback:
      - prometheus
```

Если base ConfigMap управляется отдельно:

```yaml
baseConfigMap:
  create: false
  name: litellm-base-config
  key: config.yaml
```

Официальному LiteLLM chart передайте generated ConfigMap:

```yaml
proxyConfigMap:
  create: false
  name: litellm-generated-config
  key: config.yaml
```

LiteLLM читает статический YAML при старте, поэтому изменение моделей требует
rollout. В deployment-рецепте настроены PDB и 30-минутный graceful shutdown
(`terminationGracePeriodSeconds: 1800`). DB-managed модели
(`store_model_in_db`) являются отдельным режимом и не должны одновременно
управляться этим контроллером.

## Установка

```bash
helm upgrade --install litellm-config-adapter ./charts/litellm-config-adapter \
  --namespace litellm --create-namespace \
  --set image.repository=registry.example.com/litellm-config-adapter \
  --set image.tag=0.1.0 \
  --set litellm.deploymentName=litellm
```

Полный пример развёртывания LiteLLM, vLLM, PDB, drain и CI/CD находится в
[`DEPLOYMENT_RECIPE.md`](DEPLOYMENT_RECIPE.md).

## Разработка

```bash
make test
go test -race ./...
go vet ./...
docker build -t litellm-config-adapter:dev .
make helm-lint
```

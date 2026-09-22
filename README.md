# iplist-go-unifi

[![Tests](https://github.com/dexogen/iplist-go-unifi/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/dexogen/iplist-go-unifi/actions/workflows/test.yml)
[![Build image](https://github.com/dexogen/iplist-go-unifi/actions/workflows/build-image.yml/badge.svg?branch=main)](https://github.com/dexogen/iplist-go-unifi/actions/workflows/build-image.yml)
[![GHCR](https://img.shields.io/badge/ghcr.io%2Fdexogen%2Fiplist--go--unifi-latest-blue)](https://github.com/dexogen/iplist-go-unifi/pkgs/container/iplist-go-unifi)

Сервис синхронизирует UniFi-экспорты [`iplist-go`](https://github.com/dexogen/iplist-go) с правилами Traffic Routes в UniFi Network.

## Конфигурация

За основу можно взять [examples/config.yml](examples/config.yml).

```yaml
unifi:
  base_url: "https://unifi.example.com"
  username: "iplist"
  password: "change-me"
  site: "default"

schedule:
  cron: "0 0 * * *"
  timezone: "Europe/Moscow"
  run_on_start: true

safety:
  dry_run: true
  min_entries: 10
  max_entries: 20000
  allow_empty: false
  max_removal_ratio: 0.35
  require_fresh: true
  max_source_age: "48h"

sources:
  - name: "latest-ipv4"
    url: "https://iplist.example.com/api/latest/export?format=unifi&data=ipv4"
    type: "ipv4_cidr"
    network_name: "WAN"
```

## Запуск

```bash
docker build -t iplist-go-unifi:local .
docker run --rm \
  -p 18086:18086 \
  -v ./config.yml:/etc/iplist-go-unifi/config.yml:ro \
  -v ./runtime:/var/lib/iplist-go-unifi \
  iplist-go-unifi:local
```

В runtime-volume сервис хранит бэкапы правил и локальный state с `route_id`/`hash`.

Однократная проверка без записи в UniFi:

```bash
iplist-go-unifi -config ./config.yml -once -dry-run
```

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /status`

## Защита обновлений

`max_removal_ratio` по умолчанию равен 0.35 и ограничивает долю старых записей, адресное покрытие которых исчезает. Объединение нескольких CIDR и эквивалентное разбиение сети не считаются потерей. Для доменов учитывается покрытие родительским доменом. Лимит можно переопределить в `sources[].safety`; значение 1 разрешает все удаления для подтвержденной миграции. Абсолютные ограничения `min_entries`, `max_entries`, `allow_empty` действуют дополнительно.

`require_fresh: true` требует заголовки снимка, состояния и времени источника от iplist-go. `degraded`, `stale`, отсутствующие обязательные метаданные и истекший `max_source_age` блокируют запись в UniFi. По умолчанию обязательность метаданных выключена для совместимости со сторонними источниками; если заголовки присутствуют, их состояние и возраст проверяются всегда. Для установок с iplist-go рекомендуется включить `require_fresh`.

Перед переключением источника запускайте `-once -dry-run`: этот режим проверяет свежесть, ограничения и потерю покрытия, но не меняет маршруты. `/status` показывает `snapshot`, `source_updated_at`, `uncovered` и причину блокировки. Одновременные прогоны синхронизации исключены.

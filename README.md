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

Однократная проверка без записи в UniFi:

```bash
iplist-go-unifi -config ./config.yml -once -dry-run
```

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /status`

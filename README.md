# jalvio-service

[![CI](https://github.com/lscythe/jalvio-service/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/lscythe/jalvio-service/actions/workflows/ci.yml)
[![Coverage](https://coveralls.io/repos/github/lscythe/jalvio-service/badge.svg?branch=main)](https://coveralls.io/github/lscythe/jalvio-service?branch=main)
[![Go version](https://img.shields.io/github/go-mod/go-version/lscythe/jalvio-service)](go.mod)
[![License](https://img.shields.io/github/license/lscythe/jalvio-service)](LICENSE)

Self-hosted Go backend that serves public transit data to the Jalvio app.

## Run

```sh
mise install
go run ./cmd/jalvio
```

| Variable               | Default                          | Purpose                                         |
| ---------------------- | -------------------------------- | ----------------------------------------------- |
| `JALVIO_LISTEN`        | `:8080`                          | HTTP listen address                             |
| `JALVIO_DATA_DIR`      | `./data`                         | Stored feed, MQTT client ID, and snapshots      |
| `GTFS_URL`             | official GTFS zip   | Static feed source                              |
| `GTFS_REFRESH`         | `24h`                            | How often the feed is checked                   |
| `MQTT_URL`             | `wss://broker.example.com:8084/mqtt`  | Live bus feed; `disabled` turns it off          |
| `MQTT_TLS_SERVER_NAME` | `broker.example.com`                  | Name the broker certificate is verified against |
| `MQTT_DIAL_ADDRESS`    | empty                            | `host:port` to dial instead of the URL's host   |

## Endpoints

| Path         | Response                                                            |
| ------------ | ------------------------------------------------------------------- |
| `/healthz`   | `204` once a catalog is loaded, `503` before that or while draining |
| `/v1/status` | Version, start time, catalog freshness, and live feed state         |

Errors are RFC 9457 `application/problem+json`.

## Develop

```sh
lefthook install # git hooks: format, lint, tests, commit message
mise run check   # format, lint, race tests, coverage gate, vulnerability scan
```

## License

Apache-2.0

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

| Variable               | Default  | Purpose                                                         |
| ---------------------- | -------- | --------------------------------------------------------------- |
| `GTFS_URL`             | required | Static GTFS feed (zip) to load and refresh                      |
| `GTFS_REFRESH`         | `24h`    | How often the feed is checked                                   |
| `MQTT_URL`             | empty    | Live bus feed broker (`ws://` or `wss://`); empty turns it off  |
| `MQTT_TLS_SERVER_NAME` | URL host | Name the broker certificate is verified against                 |
| `MQTT_DIAL_ADDRESS`    | empty    | `host:port` to dial instead of the URL's host                   |
| `STOP_API_URL`         | empty    | Stop API base URL for the facilities sync; empty turns it off   |
| `STOP_API_APP_VERSION` | empty    | App version the stop API expects; required with `STOP_API_URL`  |
| `STOPINFO_SYNC_AT`     | `03:00`  | Daily facilities sync time (`HH:MM`) in the local zone (`TZ`)   |
| `JALVIO_LISTEN`        | `:8080`  | HTTP listen address                                             |
| `JALVIO_DATA_DIR`      | `./data` | Stored feed, client and device IDs, and snapshots               |

Upstream endpoints are deployment configuration and are not part of this repository.

## Endpoints

| Path         | Response                                                            |
| ------------ | ------------------------------------------------------------------- |
| `/healthz`   | `204` once a catalog is loaded, `503` before that or while draining |
| `/v1/status` | Version, start time, catalog, live feed, and stop facilities state  |

Errors are RFC 9457 `application/problem+json`.

## Develop

```sh
lefthook install # git hooks: format, lint, tests, commit message
mise run check   # format, lint, race tests, coverage gate, vulnerability scan
```

## License

Apache-2.0

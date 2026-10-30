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

| Variable                     | Default   | Purpose                                                         |
| ---------------------------- | --------- | --------------------------------------------------------------- |
| `GTFS_URL`                   | required  | Static GTFS feed (zip) to load and refresh                      |
| `GTFS_REFRESH`               | `24h`     | How often the feed is checked                                   |
| `MQTT_URL`                   | empty     | Live bus feed broker (`ws://` or `wss://`); empty turns it off  |
| `MQTT_TLS_SERVER_NAME`       | URL host  | Name the broker certificate is verified against                 |
| `MQTT_DIAL_ADDRESS`          | empty     | `host:port` to dial instead of the URL's host                   |
| `MQTT_CLIENT_ID_PREFIX`      | `jalvio-` | Client ID prefix; a stored ID without it is replaced            |
| `STOP_API_URL`               | empty     | Stop API base URL for the facilities sync; empty turns it off   |
| `STOP_API_APP_VERSION`       | empty     | App version the stop API expects; required with `STOP_API_URL`  |
| `STOPINFO_SYNC_AT`           | `03:00`   | Daily facilities sync time (`HH:MM`) in the local zone (`TZ`)   |
| `PHOTON_URL`                 | empty     | Photon geocoder base URL for place search; empty turns it off   |
| `PHOTON_USER_AGENT`          | empty     | User-Agent naming the operator; required with `PHOTON_URL`      |
| `PLACES_REQUESTS_PER_MINUTE` | `60`      | Geocoder calls allowed per minute; cached lookups are free      |
| `MAX_LIVE_CONNECTIONS`       | `2000`    | Open live connections allowed at once, across every region      |
| `BROWSER_ORIGINS`            | empty     | Space-separated origins allowed on `live`; empty: apps only     |
| `LEGAL_FILE`                 | data dir  | Terms and privacy policy to publish; default `legal.json`       |
| `JALVIO_LISTEN`              | `:8080`   | HTTP listen address                                             |
| `JALVIO_DATA_DIR`            | `./data`  | Stored feed, client and device IDs, and snapshots               |

Upstream endpoints are deployment configuration and are not part of this repository.

### Docker

```sh
cp .env.example .env # then fill in the upstream endpoints
docker compose up -d --build
```

Each release is also published for `linux/amd64` and `linux/arm64` as `ghcr.io/lscythe/jalvio-service:<version>`, `:<major>.<minor>`, and `:latest`.

The image is distroless, runs as a non-root user, and keeps its state in the `/data` volume. Keep that volume across upgrades so restarts don't depend on the upstreams. `jalvio healthcheck` exits 0 when the instance is ready and backs the image's `HEALTHCHECK`.

## Endpoints

| Path                 | Response                                                                                   |
| -------------------- | ------------------------------------------------------------------------------------------ |
| `/healthz`           | `204` once every region's catalog is loaded, `503` before that or while draining           |
| `/v1/status`         | Version, start time, and each region's catalog, live feed, and stop facilities state       |
| `/v1/legal/{name}`   | The owner's `terms` or `privacy` policy, in English or Indonesian (`?lang=id`)             |

Everything specific to a region lives under `/v1/regions/{region}`; an unknown region is `404`.

| Path under the region | Response                                                                                  |
| --------------------- | ----------------------------------------------------------------------------------------- |
| `/catalog`            | The whole static network for offline use: routes, stops with facilities, patterns, fares  |
| `/catalog/shapes`     | Encoded polylines for every pattern                                                       |
| `/departures`         | Live buses expected at up to ten stops, grouped by route and headsign                     |
| `/buses`              | Live buses in a bounding box, on routes, or both                                          |
| `/buses/{number}`     | One live bus and the stops it is expected at                                              |
| `/live`               | WebSocket for the map: a snapshot per subscribe (bounding box, routes), then 1 s updates  |
| `/places`             | Destinations matching a query, in the area the region serves, with OSM attribution        |
| `/places/reverse`     | The address or place at a point                                                           |

[`api/openapi.yaml`](api/openapi.yaml) is the contract, and tests check every response against it. [`api/live.schema.json`](api/live.schema.json) does the same for `live` frames. The catalog and legal documents carry an ETag and answer a matching `If-None-Match` with `304`, so clients keep them and refresh cheaply. They are gzipped when the client accepts it. Errors are RFC 9457 `application/problem+json`.

The legal documents belong to whoever runs the instance. Write them in `LEGAL_FILE`, starting from [`legal.example.json`](legal.example.json): each document has an `updated` date, an English version, and optionally an Indonesian one, which falls back to English when missing. The file is read at startup, so restart to publish a change; an invalid file stops startup with the field at fault, and without a file both documents return `404`.

## Develop

```sh
lefthook install # git hooks: format, lint, tests, commit message
mise run check   # format, lint, race tests, coverage gate, vulnerability scan
```

`mise run live` checks the real upstreams named in the environment: one GTFS download, 15 s of the broker (`LIVE_MQTT_DURATION`), one stop API name search, and one Photon search (`PHOTON_URL`, `PHOTON_USER_AGENT`, `LIVE_PLACE_QUERY`). Each check skips when its settings are missing, and output is counts only. CI never runs it.

Releases are cut by [release-please](https://github.com/googleapis/release-please): Conventional Commits merged to `main` keep a release pull request up to date, and merging it tags `vX.Y.Z`, publishes a GitHub release, and updates `CHANGELOG.md`.

## License

Apache-2.0

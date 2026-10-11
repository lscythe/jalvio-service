# jalvio-service

Self-hosted Go backend that serves public transit data to the Jalvio app.

## Run

```sh
mise install
go run ./cmd/jalvio
```

| Variable          | Default                        | Purpose                       |
| ----------------- | ------------------------------ | ----------------------------- |
| `JALVIO_LISTEN`   | `:8080`                        | HTTP listen address           |
| `JALVIO_DATA_DIR` | `./data`                       | Stored feed and snapshots     |
| `GTFS_URL`        | official TransJakarta GTFS zip | Static feed source            |
| `GTFS_REFRESH`    | `24h`                          | How often the feed is checked |

## Endpoints

| Path         | Response                                                            |
| ------------ | ------------------------------------------------------------------- |
| `/healthz`   | `204` once a catalog is loaded, `503` before that or while draining |
| `/v1/status` | Version, start time, and catalog freshness                          |

Errors are RFC 9457 `application/problem+json`.

## Develop

```sh
mise run check   # format, lint, race tests, vulnerability scan
```

## License

Apache-2.0

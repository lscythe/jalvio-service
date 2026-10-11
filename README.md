# jalvio-service

Self-hosted Go backend that serves public transit data to the Jalvio app.

## Run

```sh
mise install
go run ./cmd/jalvio
```

| Variable        | Default | Purpose                  |
| --------------- | ------- | ------------------------ |
| `JALVIO_LISTEN` | `:8080` | HTTP listen address      |

## Endpoints

| Path         | Response                                             |
| ------------ | ---------------------------------------------------- |
| `/healthz`   | `204` when ready, `503` while starting or draining   |
| `/v1/status` | Version and start time                               |

Errors are RFC 9457 `application/problem+json`.

## Develop

```sh
mise run check   # format, lint, race tests, vulnerability scan
```

## License

Apache-2.0

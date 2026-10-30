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
| `MAX_LIVE_CONNECTIONS`       | `2000`    | Open `/v1/live` connections allowed at once                     |
| `BROWSER_ORIGINS`            | empty     | Space-separated origins allowed on `/v1/live`; empty: apps only |
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
| `/healthz`           | `204` once a catalog is loaded, `503` before that or while draining                        |
| `/v1/status`         | Version, start time, catalog, live feed, and stop facilities state                         |
| `/v1/catalog`        | The whole static network for offline use: routes, stops with facilities, patterns, fares   |
| `/v1/catalog/shapes` | Encoded polylines for every pattern                                                        |
| `/v1/departures`     | Live buses expected at up to ten stops, grouped by route and headsign                      |
| `/v1/buses`          | Live buses in a bounding box, on routes, or both                                           |
| `/v1/buses/{number}` | One live bus and the stops it is expected at                                               |
| `/v1/live`           | WebSocket for the map: a snapshot per subscribe (bounding box, routes), then 1 s updates   |
| `/v1/places`         | Destinations matching a query, in the area the network serves, with OSM attribution        |
| `/v1/places/reverse` | The address or place at a point                                                            |
| `/v1/legal/{name}`   | The owner's `terms` or `privacy` policy, in English or Indonesian (`?lang=id`)             |

[`api/openapi.yaml`](api/openapi.yaml) is the contract, and tests check every response against it. [`api/live.schema.json`](api/live.schema.json) does the same for `/v1/live` frames. The catalog and legal documents carry an ETag and answer a matching `If-None-Match` with `304`, so clients keep them and refresh cheaply. They are gzipped when the client accepts it. Errors are RFC 9457 `application/problem+json`.

The legal documents belong to whoever runs the instance. Write them in `LEGAL_FILE`, starting from [`legal.example.json`](legal.example.json): each document has an `updated` date, an English version, and optionally an Indonesian one, which falls back to English when missing. The file is read at startup, so restart to publish a change; an invalid file stops startup with the field at fault, and without a file both documents return `404`.

## Community service

`cmd/community` is a separate, optional service that gives riders a community identity: a device account created without signing in, an optional Google account linked later without losing anything, and a display name and avatar. It stores accounts in PostgreSQL and holds no email or Google profile data. Transit stays anonymous and never calls it.

```sh
docker compose --profile community up -d --build
```

This runs the community service on port `8081` with a local PostgreSQL. To run it from source, start the database with `mise run community-db` and set `COMMUNITY_DATABASE_URL`.

| Variable                      | Default  | Purpose                                                                 |
| ----------------------------- | -------- | ----------------------------------------------------------------------- |
| `COMMUNITY_DATABASE_URL`      | required | PostgreSQL connection string; a remote host needs `sslmode=verify-full` |
| `COMMUNITY_DB_MAX_CONNS`      | `5`      | Connection pool size; hosted plans cap connections                      |
| `GOOGLE_CLIENT_IDS`           | empty    | Client IDs the app requests ID tokens for; empty turns sign-in off      |
| `GOOGLE_KEYS_URL`             | empty    | Google's public key set URL; required with `GOOGLE_CLIENT_IDS`          |
| `COMMUNITY_ACCOUNTS_PER_HOUR` | `10`     | Device accounts one client address may create per hour                  |
| `COMMUNITY_LISTEN`            | `:8081`  | HTTP listen address                                                     |

| Method and path             | Response                                                                         |
| --------------------------- | -------------------------------------------------------------------------------- |
| `GET /healthz`              | `204` once the database is reachable, `503` otherwise or while draining          |
| `POST /v1/accounts`         | `201` with a token for a new device account                                      |
| `POST /v1/session/google`   | Links the account to a Google ID token, or merges it into the linked account     |
| `GET /v1/me`                | The rider's display name, avatar, whether Google is linked, and creation time    |
| `PATCH /v1/me`              | Changes the profile; a missing field is unchanged and `null` clears it           |
| `DELETE /v1/session`        | Signs this phone out of a Google-linked account                                  |
| `DELETE /v1/me`             | Deletes the account and everything tied to it                                    |

A Google ID token names the client it was issued to in its `aud` claim. On Android, and on iOS when the app passes a server client ID, that is the web (server) client ID the app passes as `serverClientId`, not the Android or iOS client ID. List that web client ID in `GOOGLE_CLIENT_IDS`, plus the iOS client ID if the iOS app requests tokens without a server client ID; otherwise every sign-in is refused with `400`.

[`api/community.openapi.yaml`](api/community.openapi.yaml) is the contract. Requests other than account creation send `Authorization: Bearer <token>`; the service stores only each token's SHA-256. Device-only accounts unused for 13 months are deleted.

Migrations are embedded and run at startup, so a new database sets itself up. They use plain PostgreSQL 13+ features in a dedicated `community` schema, so a hosted PostgreSQL such as Supabase works as a database and moves to a self-hosted one with a dump and restore:

- Every table has row-level security on and no policies, so a hosted provider's generated APIs read nothing, even if the schema is exposed. The service connects as the table owner and is unaffected.
- Use Supabase's session-mode pooler URL. Transaction mode also needs `default_query_exec_mode=simple_protocol` in the URL. The direct connection is IPv6-only on most plans.
- `sslmode=verify-full` needs the provider's CA certificate: download it from the database settings and point `sslrootcert` at it. Keep it with the deployment configuration, not in the repository.
- Supabase's free plan pauses projects after about a week without activity.

## Develop

```sh
lefthook install      # git hooks: format, lint, tests, commit message
mise run community-db # local PostgreSQL for the community tests
mise run check        # format, lint, race tests, coverage gate, vulnerability scan
```

The community store tests use the database `mise run community-db` starts, creating and dropping one database per test. Without it they skip, and the coverage gate then fails for those packages; CI always runs them against a PostgreSQL service container.

`mise run live` checks the real upstreams named in the environment: one GTFS download, 15 s of the broker (`LIVE_MQTT_DURATION`), one stop API name search, and one Photon search (`PHOTON_URL`, `PHOTON_USER_AGENT`, `LIVE_PLACE_QUERY`). Each check skips when its settings are missing, and output is counts only. CI never runs it.

Releases are cut by [release-please](https://github.com/googleapis/release-please): Conventional Commits merged to `main` keep a release pull request up to date, and merging it tags `vX.Y.Z`, publishes a GitHub release, and updates `CHANGELOG.md`.

## License

Apache-2.0

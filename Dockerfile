# syntax=docker/dockerfile:1
# The build stage runs natively and cross-compiles for each target platform.
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm@sha256:21ade014faf952365b9ecbd8dbdb87176fbacb82fac9fb132d9f2a7b8cc3f132 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/jalvio ./cmd/jalvio \
    && mkdir /out/data

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/jalvio /jalvio
# A new named volume takes this directory's ownership, so the nonroot user can write to it.
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV JALVIO_LISTEN=:8080 JALVIO_DATA_DIR=/data
EXPOSE 8080
VOLUME /data
HEALTHCHECK --interval=15s --timeout=5s --start-period=60s --retries=3 CMD ["/jalvio", "healthcheck"]
ENTRYPOINT ["/jalvio"]

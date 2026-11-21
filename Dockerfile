# bitacora-hub container image (ADR-0002). The web UI is embedded into the
# binary via go:embed (internal/webui/dist/, already built and committed),
# so this is a plain Go build — no separate frontend build stage needed.
FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# CGO_ENABLED=0: the project deliberately uses modernc.org/sqlite (pure
# Go, no CGO) — see CONTRIBUTING.md's "no dependency that requires CGO by
# default in the agent", which applies equally here.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bitacora-hub ./cmd/bitacora-hub

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/bitacora-hub /usr/local/bin/bitacora-hub

# ADR-0003: all hub data (SQLite relational store + metrics tsdb) lives
# under one base directory — mount this as a volume for real persistence.
VOLUME /var/lib/bitacora
# ADR-0025: the local credential, the key that encrypts its TOTP secret and
# the setup token state live under /etc/bitacora by default. They must be on a
# persistent named volume or bind mount: an anonymous volume is lost when the
# container is recreated, which sends the hub back to first-run setup.
VOLUME /etc/bitacora
EXPOSE 8081

ENTRYPOINT ["/usr/local/bin/bitacora-hub", "-addr=0.0.0.0:8081", "-data-dir=/var/lib/bitacora"]

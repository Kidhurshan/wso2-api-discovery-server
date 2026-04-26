# Multi-stage build per claude/specs/operations_guide.md §9.1.
#
# Build stage: Go 1.22 alpine, statically linked binary stripped of debug
# info via -s -w. Final stage: alpine 3.19 + ca-certificates + tzdata + a
# non-root user.

FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
    -o /ads ./cmd/ads

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 ads
COPY --from=build /ads /usr/local/bin/ads
USER 1000
EXPOSE 8443 9090
ENTRYPOINT ["/usr/local/bin/ads"]
CMD ["--config", "/etc/ads/config.toml"]

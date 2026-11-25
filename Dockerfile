# syntax=docker/dockerfile:1

ARG SUPERVISOR_VERSION=0.1.0

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG SUPERVISOR_VERSION
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOTOOLCHAIN=local GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags "-s -w -X main.version=${SUPERVISOR_VERSION}" \
    -o /out/dmd ./cmd/dmd

FROM alpine:3.24
ARG SUPERVISOR_VERSION
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates tzdata \
    && mkdir -p /secrets \
    && chmod 0755 /secrets
COPY --from=build /out/dmd /usr/bin/dmd
RUN chmod 0755 /usr/bin/dmd && /usr/bin/dmd version
LABEL org.opencontainers.image.title=dreame-dashboard \
      org.opencontainers.image.description="LAN dashboard for a Dreame air purifier. Not an official Dreame image." \
      org.opencontainers.image.version="${SUPERVISOR_VERSION}" \
      org.opencontainers.image.source=https://github.com/lostinfuture/dreame-dashboard \
      org.opencontainers.image.licenses="Apache-2.0"
EXPOSE 9094
USER 65532:65532
HEALTHCHECK --interval=60s --timeout=5s --start-period=30s --start-interval=5s --retries=3 \
    CMD ["/usr/bin/dmd", "healthcheck"]
ENTRYPOINT ["/usr/bin/dmd"]
CMD ["run"]

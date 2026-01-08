FROM --platform=$BUILDPLATFORM golang:1.24 AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY main.go ./main.go
COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

RUN set -eu; \
    mkdir -p /app/bin; \
    if [ "${TARGETARCH}" = "arm" ]; then \
      case "${TARGETVARIANT}" in \
        v6) GOARM="6" ;; \
        v7) GOARM="7" ;; \
        *) echo "Unsupported TARGETVARIANT=${TARGETVARIANT} for arm" >&2; exit 1 ;; \
      esac; \
      CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" GOARM="${GOARM}" \
        go build -trimpath -ldflags="-s -w" -o /app/bin/mtsh main.go; \
    else \
      CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
        go build -trimpath -ldflags="-s -w" -o /app/bin/mtsh main.go; \
    fi

FROM alpine:3.20

COPY --from=builder /app/bin/mtsh /usr/local/bin/mtsh

ENTRYPOINT ["/usr/local/bin/mtsh"]
CMD ["server", "-v", "--dm-only"]

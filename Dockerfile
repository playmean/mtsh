FROM golang:1.24 AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY main.go ./main.go
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o ./bin/mtsh main.go

FROM ubuntu:24.04

COPY --from=builder /app/bin/mtsh /usr/local/bin/mtsh

ENTRYPOINT [ "/usr/local/bin/mtsh" ]

CMD [ "server", "-v", "--dm-only" ]

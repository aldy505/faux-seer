# syntax=docker/dockerfile:1

FROM golang:1.24.0-trixie AS builder

RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential ca-certificates pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 go build -o /out/faux-seer ./cmd/faux-seer

FROM debian:trixie-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/faux-seer /usr/local/bin/faux-seer

EXPOSE 9091

CMD ["faux-seer"]

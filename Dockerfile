# --- Build stage ---
FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /deezbytes .

# --- Final stage ---
FROM alpine:latest

RUN apk --no-cache add ca-certificates

COPY --from=builder /deezbytes /deezbytes

ENV CONFIG_PATH=/config.yml

WORKDIR /

EXPOSE 9101

ENTRYPOINT ["/deezbytes"]

# syntax=docker/dockerfile:1.7

FROM golang:1.26.4-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/reporting ./cmd/reporting

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
	&& addgroup -S app \
	&& adduser -S app -G app

WORKDIR /app

COPY --from=builder /out/reporting /app/reporting

USER app
EXPOSE 3003

ENTRYPOINT ["/app/reporting"]

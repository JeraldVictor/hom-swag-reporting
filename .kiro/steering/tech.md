# Tech Stack

## Runtime

- **Language**: Go
- **Module**: `github.com/JeraldVictor/hom-swag-reporting`
- **Go Version**: `go 1.25.0` in `go.mod`; container builds use `golang:1.26.4-alpine`.
- **Local Reload**: Air via `.air.toml`

## Core Dependencies

- **Kafka**: `github.com/segmentio/kafka-go` for request consumption and status/dead-letter production.
- **MongoDB**: `go.mongodb.org/mongo-driver` for source data aggregation.
- **MinIO**: `github.com/minio/minio-go/v7` for artifact upload.
- **XLSX**: `github.com/xuri/excelize/v2` for spreadsheet generation.
- **UUID**: `github.com/google/uuid` for event IDs.
- **Env Loading**: `github.com/joho/godotenv` for local `.env` support.

## Renderers

- CSV writer for `text/csv`.
- XLSX writer for `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`.
- Lightweight in-repo PDF writer for `application/pdf`.

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `3003` | HTTP health/preview server port |
| `MONGODB_URI` | local MongoDB URI | MongoDB connection string |
| `MONGO_DATABASE` | `homswag` | MongoDB database name |
| `KAFKA_BROKERS` | `127.0.0.1:9094` | Comma-separated Kafka broker list |
| `REPORTING_REQUEST_TOPIC` | `homswag.reporting.requests` | Job request topic |
| `REPORTING_EVENT_TOPIC` | `homswag.reporting.events` | Status event topic |
| `REPORTING_DEAD_LETTER_TOPIC` | `homswag.reporting.dead-letter` | Dead-letter topic |
| `REPORTING_CONSUMER_GROUP` | `homswag-reporting-workers` | Kafka consumer group |
| `MINIO_ENDPOINT` | `127.0.0.1:9000` | MinIO endpoint; include port or set `MINIO_PORT` separately |
| `MINIO_PORT` | empty | Optional MinIO port appended when `MINIO_ENDPOINT` has no port |
| `MINIO_ACCESS_KEY` | `minioadmin` | MinIO access key |
| `MINIO_SECRET_KEY` | `minioadmin` | MinIO secret key |
| `MINIO_USE_SSL` | `false` | MinIO TLS toggle |
| `REPORTING_BUCKET` | `reports` | MinIO bucket for artifacts |
| `REPORTING_MAX_ROWS` | `200000` | Intended maximum report row count |
| `REPORTING_JOB_TTL_DAYS` | `30` | Intended report job retention |
| `REPORTING_SIGNED_URL_TTL_SECONDS` | `900` | Intended signed URL TTL |

## Common Commands

```bash
go run ./cmd/reporting
go build -o ./tmp/main ./cmd/reporting
go test ./...
air
```

## Container And Deployment

- Container image names follow the workspace convention: deploy pulls `docker.io/jeraldvictor/hom-swag-reporting:<tag>`, while release pushes may target `registry.digitalocean.com/homswag-repo/hom-swag-reporting:<tag>`.
- `Containerfile` builds a static Linux binary with `golang:1.26.4-alpine` and runs it from Alpine on port `3003`.
- `deploy/compose.yaml` runs this image as the `reporting` service with MongoDB, MinIO, and Kafka dependencies.

## Notes

- Keep `go.mod` as the source of truth for the Go version.
- Do not commit `.env`, `.gocache`, `tmp`, generated artifacts, or local logs.
- The service currently has no package-level test suite; add `go test` coverage around new executor logic and renderers when behavior changes.

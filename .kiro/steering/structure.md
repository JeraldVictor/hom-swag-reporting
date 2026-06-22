# Project Structure

```
reporting/
├── cmd/
│   └── reporting/
│       └── main.go              # Service entry point, dependency wiring, HTTP health/preview server, executor registration
├── internal/
│   ├── config/
│   │   └── config.go            # Environment parsing and defaults
│   ├── jobs/
│   │   ├── jobs.go              # Job-related shared types/helpers
│   │   └── worker.go            # Kafka consumer loop, report execution, artifact upload, status events
│   ├── kafka/
│   │   ├── events.go            # Report request/status/dead-letter event payloads
│   │   └── kafka.go             # Kafka consumer/producer wrappers
│   ├── minio/
│   │   └── minio.go             # MinIO connection and upload helpers
│   ├── mongo/
│   │   └── mongo.go             # MongoDB connection wrapper
│   ├── render/
│   │   ├── csv.go               # CSV row writer
│   │   ├── pdf.go               # Lightweight PDF row writer
│   │   └── xlsx.go              # XLSX row writer
│   └── reports/
│       ├── registry.go          # Executor interface and registry lookup by key/version
│       └── static/              # Static report executors
│           ├── beautician_commission.go
│           ├── daily_sales.go
│           ├── payment_expr.go       # Shared MongoDB payment aggregation expressions
│           ├── petrol_weekly.go
│           ├── rider_commission.go
│           └── staff_summary.go
├── .air.toml                    # Local live-reload config
├── Containerfile                 # Multi-stage Go container build
├── go.mod
├── go.sum
└── README.md
```

## Architectural Patterns

- The service is intentionally small: `main.go` wires dependencies, registers executors, starts the HTTP server, and starts the Kafka worker.
- Report executors implement `reports.Executor` with `Key()`, `Version()`, `Validate()`, and `Run()`.
- Executors write rows to `reports.RowSink`; renderer selection stays in `jobs.Worker`.
- Shared MongoDB aggregation expressions used by multiple reports live beside the static executors.
- Job processing emits status events rather than mutating server state directly.
- Temporary files are written under `/tmp/reports` and removed after upload.
- The worker processes consumed jobs concurrently by starting a goroutine per valid request.

## Adding A Static Report

1. Add an executor under `internal/reports/static/`.
2. Implement the `reports.Executor` interface.
3. Register it in `cmd/reporting/main.go`.
4. Add or update the matching server report definition seeder in `server/src/scripts/seeders/report-definitions.ts`.
5. Keep `report_key`, `version`, expected parameters, and supported formats aligned with the server/admin UI.

## Conventions

- Keep package names short and domain-specific.
- Use `context.Context` for database, Kafka, MinIO, and executor work.
- Return errors from executors; let `jobs.Worker` translate them into failed status events.
- Prefer streaming row output through `RowSink` over accumulating large result sets.
- Keep environment defaults local-development friendly, but production values must come from the deploy environment.

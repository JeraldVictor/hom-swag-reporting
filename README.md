# HomSwag Reporting Service

Dedicated Go service for heavy report execution and file generation.

## Architecture

- **Go 1.26.4**
- **Kafka** for job requests and status events.
- **MongoDB** for data aggregation.
- **MinIO** for report artifact storage.
- **XLSX & CSV** renderers with streaming support.

## Project Structure

- `cmd/reporting/`: Entry point and initialization.
- `internal/config/`: Environment configuration.
- `internal/jobs/`: Worker logic and job orchestration.
- `internal/kafka/`: Kafka consumer and producer.
- `internal/mongo/`: MongoDB client and helpers.
- `internal/minio/`: MinIO client and upload logic.
- `internal/reports/`: Report executor registry and implementations.
  - `static/`: Pre-defined report executors (Go code).
  - `dynamic/`: Dynamic report executors (Safe query builders).
  - `datasets/`: Approved dataset definitions for dynamic reports.
- `internal/render/`: CSV and XLSX output writers.

## Implemented Static Reports

1. `rider_commission`: Rider commission summaries.
2. `beautician_commission`: Beautician commission summaries.
3. `petrol_weekly`: Weekly rider petrol calculation.
4. `daily_sales`: Daily sales trends and summaries.
5. `staff_summary`: Staff leave and overtime summary.

## Running Locally

1. Ensure Kafka, MongoDB, and MinIO are running (use `deploy/compose.yaml`).
2. Set environment variables (see `reporting/internal/config/config.go`).
3. Run the service with live reload (recommended for development):
   ```bash
   cd reporting
   # Install air if you haven't: go install github.com/air-verse/air@latest
   air
   ```
4. Or run directly:
   ```bash
   cd reporting
   go run cmd/reporting/main.go
   ```

## Development

To add a new static report:
1. Create a new executor in `internal/reports/static/`.
2. Implement the `Executor` interface.
3. Register the executor in `cmd/reporting/main.go`.
4. Add the report definition to the server seeder in `server/src/scripts/seeders/report-definitions.ts`.

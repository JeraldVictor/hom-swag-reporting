# Product Overview

The HomSwag Reporting service is a dedicated Go worker/API service for heavy report execution and artifact generation. It keeps expensive report generation out of the Node.js server by consuming report jobs from Kafka, querying MongoDB, rendering files, uploading them to MinIO, and publishing status events back to Kafka.

## Core Domain Concepts

- **Report Job**: A request emitted by the server to generate a report for a staff user, office, report key, version, format, and parameter set.
- **Report Definition**: Metadata owned by the server/admin system; static executors in this service must match seeded report keys and versions.
- **Executor Registry**: In-process registry mapping `report_key` + `version` to a Go executor implementation.
- **Row Sink**: Streaming-oriented writer interface used by executors so CSV, XLSX, and PDF renderers can share the same report logic.
- **Artifacts**: Generated report files stored in MinIO under date/job-based keys.
- **Status Events**: Kafka events emitted while a job moves through stages such as initializing, querying, uploading, completed, or failed.
- **Dead Letter Topic**: Kafka topic used for malformed/unprocessable request payloads.

## Implemented Static Reports

1. `rider_commission` - rider commission summaries.
2. `beautician_commission` - beautician commission summaries.
3. `petrol_weekly` - weekly rider petrol calculations.
4. `daily_sales` - daily sales trends and summaries.
5. `staff_summary` - staff leave and overtime summary.
6. `customer_booking` - customer last-booking and saved-address zone report.
7. `product_insights` - ranked product quantities, order counts, catalog hierarchy, and gross sales.

## Key Flows

1. Server creates a report job and publishes a `ReportRequest` to `homswag.reporting.requests`.
2. Reporting worker consumes the Kafka message and looks up the matching executor.
3. Executor streams rows to the selected renderer: CSV, XLSX, or PDF.
4. Worker uploads the generated artifact to MinIO under `reports/<year>/<month>/<job_id>/...`.
5. Worker publishes progress/completion/failure events to `homswag.reporting.events`.
6. Malformed request messages are written to `homswag.reporting.dead-letter`.

## HTTP Surface

- `GET /health` returns `OK` for container/deploy health checks.
- `POST /preview` runs an executor in-memory and returns JSON rows for preview/debug use. It accepts `report_key`, `version`, optional `office_id`, `parameters`, and `limit`.

## External Dependencies

- Kafka for request, status, and dead-letter topics.
- MongoDB for source data aggregation.
- MinIO for generated report artifacts.
- Server seeders define report definitions that must stay aligned with executor keys/versions.

# HomSwag Reporting Service

Dedicated Go service for heavy report execution and file generation.

The service also owns the new earnings control plane. It is introduced in
`shadow` mode so ledger reconstruction can be compared with the current system
before any production payout reads are switched over.

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
- `internal/earnings/`: Authenticated earnings ledger and administrative controls.

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

## Earnings control plane

Set the same `JWT_SECRET` used by the TypeScript server. Admin requests use the
logged-in staff JWT and an `office_id`; the old shared reporting token is not
accepted for earnings writes.

Environment:

- `EARNINGS_MODE=shadow` keeps the ledger isolated from production payouts.
- `EARNINGS_MODE=authoritative` is reserved for the final cutover after parity
  checks and payout allocation support are complete.
- `EARNINGS_SOURCE_TOPIC=homswag.earnings.sources` carries persisted order and
  trip snapshot notifications.
- `EARNINGS_SOURCE_CONSUMER_GROUP=homswag-reporting-earnings-sources` isolates
  source ingestion from report-generation consumers.

Permissions:

- `ledger.read`: service status, summaries, and ledger entries.
- `ledger.payout`: manual adjustments and period closure.
- `ledger.rebuild`: queue source-to-ledger reconstruction requests.

All money in the ledger and write API is stored as integer paise. Mutation
requests are idempotent, office-scoped, and attributed to the authenticated
staff member. A closed period rejects back-dated adjustments; corrections must
be posted into an open period.

Payout settlement is owned by `POST /api/earnings/settlements`, with history at
`GET /api/earnings/settlements`. Settlement creation atomically allocates an
integer-paise payment to the worker's open ledger entries. It rejects
overpayments, cross-office workers, active rebuild ranges, and any overlap with
a closed period. An idempotency-key replay returns the original settlement
without allocating twice.

The reconstruction endpoint records an auditable job request. The reporting
worker claims queued jobs atomically and materializes commission and petrol
snapshots into the ledger. Rebuilds are idempotent and retain source snapshots
for audit. Leaderboard rebuilds deterministically materialize rank/bonus awards
and retain the effective prize schedule, a content-addressed configuration
version, and the ranking/tie-break contract on every award. Replaying the same
logical office/period/worker award is idempotent; a changed calculation or
configuration is surfaced as a rebuild conflict instead of creating a duplicate
payable. Production parity verification must still be completed before switching
`EARNINGS_MODE` to `authoritative`.

### Earnings source events

The source-event topic accepts schema version 1 notifications. Notifications
do not carry monetary values: Go reloads the persisted source and snapshot from
Mongo through an idempotent, single-day `commissions` rebuild. Invalid events
and attempts to mutate closed earning periods go to the reporting dead-letter
topic. Mongo failures are retried without committing the Kafka offset.

```json
{
  "event_id": "019c...",
  "event_type": "earnings.source.changed",
  "schema_version": 1,
  "source_type": "order",
  "source_id": "507f1f77bcf86cd799439011",
  "source_version": "2026-07-21T12:29:59Z",
  "office_id": "507f1f77bcf86cd799439012",
  "service_date": "2026-07-21",
  "actor_id": "507f1f77bcf86cd799439013",
  "occurred_at": "2026-07-21T12:30:00Z"
}
```

`source_version` is the persisted commission/payable snapshot's
`captured_at`. The producer should write this notification through its Mongo
outbox in the same transaction as the source mutation; direct best-effort
Kafka publication is not a reliable delivery boundary.

## Development

To add a new static report:
1. Create a new executor in `internal/reports/static/`.
2. Implement the `Executor` interface.
3. Register the executor in `cmd/reporting/main.go`.
4. Add the report definition to the server seeder in `server/src/scripts/seeders/report-definitions.ts`.

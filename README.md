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
- `internal/leaderboard/`: Canonical beautician and rider ranking API for admin and field clients.

## Implemented Static Reports

1. `rider_commission`: Rider commission summaries.
2. `beautician_commission`: Beautician commission summaries.
3. `petrol_weekly`: Weekly rider petrol calculation.
4. `daily_sales`: Daily sales trends and summaries.
5. `staff_summary`: Staff leave and overtime summary.
6. `customer_booking`: Customer last-booking and saved-address zone report with configurable filters and columns.
7. `product_insights`: Ranked product quantities with order counts, catalog hierarchy filters, and sales values.

## Leaderboard API

`POST /leaderboard` is the single calculation path for admin and field
leaderboards. It owns period boundaries, source aggregation, complaint
deductions, deterministic tie-breaking, office prize lookup, field permission
checks, masking, and self-entry selection. The TypeScript server only validates
its client-facing request, resolves the authenticated user's office for field
requests, and proxies this endpoint.

When `REPORTING_API_TOKEN` is configured, the TypeScript server and reporting
service must use the same value. Supported periods are `weekly`, `monthly`,
`yearly`, `financial_year`, and `all_time`; supported roles are `beautician`
and `rider`.

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
accepted for earnings writes. In the local multi-repo workspace,
`JWT_SECRET_ENV_FILE=../server/.env` may be used instead; reporting reads only
the `JWT_SECRET` entry and direct environment injection always takes precedence.

Environment:

- `EARNINGS_MODE=shadow` is the safe default for offices without persisted mode
  configuration.
- `EARNINGS_ALLOW_NON_TRANSACTIONAL_WRITES=false` must remain disabled in
  production. Local developers using a standalone MongoDB may explicitly set
  it to `true`; replica-set deployments continue to use atomic transactions.
- `EARNINGS_MODE=authoritative` may be used as an initial deployment fallback,
  but office-specific mode changes should be performed through the authenticated
  admin cutover control. Rider commission, beautician commission, and petrol
  weekly reports resolve the persisted office mode on every execution.
- `EARNINGS_SOURCE_TOPIC=homswag.earnings.sources` carries persisted order and
  trip snapshot notifications.
- `EARNINGS_SOURCE_CONSUMER_GROUP=homswag-reporting-earnings-sources` isolates
  source ingestion from report-generation consumers.

Permissions:

- `ledger.read`: service status, summaries, and ledger entries.
- `ledger.payout`: manual adjustments and period closure.
- `ledger.rebuild`: queue source-to-ledger reconstruction requests.
- `ledger.cutover`: promote an office to authoritative mode or return it to
  shadow mode.

`POST /api/earnings/mode` changes mode for one office. Promotion to
`authoritative` is rejected while a rebuild overlaps the verification range or
when reconciliation contains any mismatch or missing snapshot. Returning to
`shadow` remains available as an immediate rollback. Every successful change
stores the staff actor, reason, verification range, timestamp, and previous
mode in the office's mode history.

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
payable. Production parity verification must still be completed before promoting
an office to authoritative mode.

`GET /api/earnings/reconciliation?office_id=<id>&start_date=YYYY-MM-DD&end_date=YYYY-MM-DD`
recalculates canonical order, trip, target, and exact-period leaderboard
earnings from source snapshots and compares them with the ledger by worker,
component, bucket, and leaderboard category. Manual adjustments are excluded so
they cannot conceal a migration discrepancy. The response is ready for cutover
only when every amount matches and no source snapshots are missing.

### Order reconciliation

The reporting service also owns the order-integrity workflow used by the admin
Order Issues page:

- `POST /api/earnings/order-issues/scan` (`ledger.rebuild`) scans completed
  orders in a date range and idempotently recreates the persisted issue index.
  This is safe to run after every production database restore. Previously open
  issues that no longer reproduce are closed automatically.
- `GET /api/earnings/order-issues` (`ledger.read`) lists office-scoped issues
  with date, status, type, severity, order-number, and pagination filters.
- `POST /api/earnings/order-issues/{id}/actions` rechecks an issue, accepts an
  explained variance (`ledger.rebuild`), or aligns a payment record
  (`ledger.payout`). Every non-recheck action requires an audit reason.

Payment alignment is append-only: Go writes one signed reconciliation entry to
the order's payment history and never edits or deletes the original payment
events. The issue ID makes the correction idempotent. Daily Sales payment
buckets include these signed entries, so subsequent reports reflect the audited
correction. Orders with legacy payment fields but no history must be fixed at
source and cannot be aligned automatically.

Order scans also validate commission-era records. They flag missing or deleted
beautician assignments and missing or invalid commission snapshots. These
issues are review-only because employee identity and service-level commission
rules cannot be safely inferred from payment totals.

### Trip reconciliation

The Trip Issues workflow mirrors Order Issues for rider payables:

- `POST /api/earnings/trip-issues/scan` (`ledger.rebuild`) scans payable
  completed trips and persists worker, distance, petrol, commission, snapshot,
  and office-rate discrepancies.
- `GET /api/earnings/trip-issues` (`ledger.read`) lists office-scoped issues
  with date, status, type, severity, trip/employee search, and pagination.
- `POST /api/earnings/trip-issues/{id}/actions` rechecks an issue, accepts an
  explained variance, or rebuilds an unpaid payable snapshot (`ledger.payout`).

Snapshot repair is deterministic and audited. It recalculates payable distance,
petrol, and trip commission from current trip and office inputs. Paid snapshots
are immutable, and worker identity or assignment changes are never performed
automatically.

Rider commission exports include both `Total Commission` and `Total Rider
Payable`; the latter explicitly adds petrol reimbursement, trip commission, and
leaderboard bonus.

### Earnings source events

The source-event topic accepts schema version 1 notifications. Notifications
do not carry monetary values: Go reloads the exact persisted order or trip and
materializes its snapshot directly into the ledger. Order events also evaluate
the affected beautician-month so crossing target 1 materializes general
commission for earlier eligible orders and crossing target 2 creates the
month-scoped bonus. No day-wide rebuild is queued. Invalid events and attempts
to mutate closed earning periods go to the reporting dead-letter topic. Mongo
failures and events whose snapshot version has not reached Mongo yet are
retried without committing the Kafka offset; stale events are safely ignored.

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

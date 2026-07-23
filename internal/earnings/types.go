package earnings

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	ModeShadow        = "shadow"
	ModeAuthoritative = "authoritative"
)

type ModeChange struct {
	PreviousMode       string             `bson:"previous_mode" json:"previous_mode"`
	Mode               string             `bson:"mode" json:"mode"`
	Reason             string             `bson:"reason" json:"reason"`
	ChangedBy          primitive.ObjectID `bson:"changed_by" json:"changed_by"`
	ChangedAt          time.Time          `bson:"changed_at" json:"changed_at"`
	ReconciliationFrom string             `bson:"reconciliation_from,omitempty" json:"reconciliation_from,omitempty"`
	ReconciliationTo   string             `bson:"reconciliation_to,omitempty" json:"reconciliation_to,omitempty"`
}

type ModeState struct {
	OfficeID  primitive.ObjectID `bson:"office_id" json:"office_id"`
	Mode      string             `bson:"mode" json:"mode"`
	UpdatedBy primitive.ObjectID `bson:"updated_by,omitempty" json:"updated_by,omitempty"`
	UpdatedAt time.Time          `bson:"updated_at,omitempty" json:"updated_at,omitempty"`
	History   []ModeChange       `bson:"history,omitempty" json:"history,omitempty"`
}

type Component string

const (
	ComponentSpecialCommission    Component = "special_commission"
	ComponentGeneralCommission    Component = "general_commission"
	ComponentUpgradeCommission    Component = "upgrade_addon_commission"
	ComponentTripCommission       Component = "trip_commission"
	ComponentPetrol               Component = "petrol"
	ComponentTargetBonus          Component = "target_bonus"
	ComponentLeaderboardBonus     Component = "leaderboard_bonus"
	ComponentTip                  Component = "tip"
	ComponentCommissionAdjustment Component = "commission_adjustment"
	ComponentPetrolAdjustment     Component = "petrol_adjustment"
	ComponentComplaintDeduction   Component = "complaint_deduction"
	ComponentReversal             Component = "reversal"
)

type SettlementBucket string

const (
	BucketCommission SettlementBucket = "commission"
	BucketPetrol     SettlementBucket = "petrol"
)

type EntryStatus string

const (
	StatusOpen             EntryStatus = "open"
	StatusPartiallySettled EntryStatus = "partially_settled"
	StatusSettled          EntryStatus = "settled"
	StatusVoid             EntryStatus = "void"
)

type LedgerEntry struct {
	ID                    primitive.ObjectID     `bson:"_id,omitempty" json:"id"`
	OfficeID              primitive.ObjectID     `bson:"office_id" json:"office_id"`
	WorkerID              primitive.ObjectID     `bson:"worker_id" json:"worker_id"`
	WorkerType            string                 `bson:"worker_type" json:"worker_type"`
	ServiceDateKey        string                 `bson:"service_date_key" json:"service_date_key"`
	Component             Component              `bson:"component" json:"component"`
	SettlementBucket      SettlementBucket       `bson:"settlement_bucket" json:"settlement_bucket"`
	AmountPaise           int64                  `bson:"amount_paise" json:"amount_paise"`
	SettledAmountPaise    int64                  `bson:"settled_amount_paise" json:"settled_amount_paise"`
	Status                EntryStatus            `bson:"status" json:"status"`
	SourceType            string                 `bson:"source_type" json:"source_type"`
	SourceID              *primitive.ObjectID    `bson:"source_id,omitempty" json:"source_id,omitempty"`
	ReversesEntryID       *primitive.ObjectID    `bson:"reverses_entry_id,omitempty" json:"reverses_entry_id,omitempty"`
	CalculationVersion    int                    `bson:"calculation_version" json:"calculation_version"`
	ConfigurationSnapshot map[string]interface{} `bson:"configuration_snapshot,omitempty" json:"configuration_snapshot,omitempty"`
	Reason                string                 `bson:"reason,omitempty" json:"reason,omitempty"`
	IdempotencyKey        string                 `bson:"idempotency_key" json:"idempotency_key"`
	CreatedBy             primitive.ObjectID     `bson:"created_by" json:"created_by"`
	CreatedAt             time.Time              `bson:"created_at" json:"created_at"`
	UpdatedAt             time.Time              `bson:"updated_at" json:"updated_at"`
}

type Period struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	OfficeID  primitive.ObjectID `bson:"office_id" json:"office_id"`
	Kind      string             `bson:"kind" json:"kind"`
	StartDate string             `bson:"start_date" json:"start_date"`
	EndDate   string             `bson:"end_date" json:"end_date"`
	Status    string             `bson:"status" json:"status"`
	ClosedBy  primitive.ObjectID `bson:"closed_by" json:"closed_by"`
	ClosedAt  time.Time          `bson:"closed_at" json:"closed_at"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

type RebuildJob struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	OfficeID         primitive.ObjectID `bson:"office_id" json:"office_id"`
	StartDate        string             `bson:"start_date" json:"start_date"`
	EndDate          string             `bson:"end_date" json:"end_date"`
	Scope            string             `bson:"scope" json:"scope"`
	Status           string             `bson:"status" json:"status"`
	IdempotencyKey   string             `bson:"idempotency_key" json:"idempotency_key"`
	RequestedBy      primitive.ObjectID `bson:"requested_by" json:"requested_by"`
	RequestedAt      time.Time          `bson:"requested_at" json:"requested_at"`
	StartedAt        *time.Time         `bson:"started_at,omitempty" json:"started_at,omitempty"`
	FinishedAt       *time.Time         `bson:"finished_at,omitempty" json:"finished_at,omitempty"`
	Scanned          int64              `bson:"scanned" json:"scanned"`
	Inserted         int64              `bson:"inserted" json:"inserted"`
	Unchanged        int64              `bson:"unchanged" json:"unchanged"`
	Conflicts        int64              `bson:"conflicts" json:"conflicts"`
	MissingSnapshots int64              `bson:"missing_snapshots" json:"missing_snapshots"`
	ErrorMessage     string             `bson:"error_message,omitempty" json:"error_message,omitempty"`
	UpdatedAt        time.Time          `bson:"updated_at" json:"updated_at"`
}

type RebuildFilter struct {
	OfficeID string
	Status   string
	Page     int64
	Limit    int64
}

type SettlementAllocation struct {
	EntryID     primitive.ObjectID `bson:"entry_id" json:"entry_id"`
	AmountPaise int64              `bson:"amount_paise" json:"amount_paise"`
}

type SettlementRevision struct {
	AmountPaise   int64                  `bson:"amount_paise" json:"amount_paise"`
	PaymentMethod string                 `bson:"payment_method" json:"payment_method"`
	Reference     string                 `bson:"reference,omitempty" json:"reference,omitempty"`
	Remarks       string                 `bson:"remarks,omitempty" json:"remarks,omitempty"`
	Allocations   []SettlementAllocation `bson:"allocations" json:"allocations"`
	EditedBy      primitive.ObjectID     `bson:"edited_by" json:"edited_by"`
	EditedAt      time.Time              `bson:"edited_at" json:"edited_at"`
}

type Settlement struct {
	ID                primitive.ObjectID     `bson:"_id,omitempty" json:"id"`
	OfficeID          primitive.ObjectID     `bson:"office_id" json:"office_id"`
	WorkerID          primitive.ObjectID     `bson:"worker_id" json:"worker_id"`
	WorkerType        string                 `bson:"worker_type" json:"worker_type"`
	Bucket            SettlementBucket       `bson:"bucket" json:"bucket"`
	StartDate         string                 `bson:"start_date" json:"start_date"`
	EndDate           string                 `bson:"end_date" json:"end_date"`
	AmountPaise       int64                  `bson:"amount_paise" json:"amount_paise"`
	PaymentMethod     string                 `bson:"payment_method" json:"payment_method"`
	Reference         string                 `bson:"reference,omitempty" json:"reference,omitempty"`
	Remarks           string                 `bson:"remarks,omitempty" json:"remarks,omitempty"`
	IdempotencyKey    string                 `bson:"idempotency_key" json:"idempotency_key"`
	Allocations       []SettlementAllocation `bson:"allocations" json:"allocations"`
	CreatedBy         primitive.ObjectID     `bson:"created_by" json:"created_by"`
	CreatedAt         time.Time              `bson:"created_at" json:"created_at"`
	UpdatedBy         primitive.ObjectID     `bson:"updated_by,omitempty" json:"updated_by,omitempty"`
	UpdatedAt         time.Time              `bson:"updated_at,omitempty" json:"updated_at,omitempty"`
	RevisionHistory   []SettlementRevision   `bson:"revision_history,omitempty" json:"revision_history,omitempty"`
	RequestedEntryIDs []primitive.ObjectID   `bson:"-" json:"-"`
}

type SettlementUpdate struct {
	AmountPaise   int64
	PaymentMethod string
	Reference     string
	Remarks       string
	UpdatedBy     primitive.ObjectID
}

type SettlementFilter struct {
	OfficeID  string
	WorkerID  string
	Bucket    string
	StartDate string
	EndDate   string
	Page      int64
	Limit     int64
}

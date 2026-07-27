package earnings

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	TripIssueMissingWorker      = "missing_worker"
	TripIssueDeletedWorker      = "deleted_worker_profile"
	TripIssueMissingSnapshot    = "missing_payable_snapshot"
	TripIssueInvalidConfig      = "invalid_payable_configuration"
	TripIssueDistanceMismatch   = "payable_distance_mismatch"
	TripIssuePetrolMismatch     = "petrol_payable_mismatch"
	TripIssueCommissionMismatch = "trip_commission_mismatch"
	TripIssueActionRecheck      = "recheck"
	TripIssueActionAccept       = "accept_variance"
	TripIssueActionRebuild      = "rebuild_payable_snapshot"
)

type TripIssueResolution struct {
	Action  string             `bson:"action" json:"action"`
	Reason  string             `bson:"reason,omitempty" json:"reason,omitempty"`
	ActorID primitive.ObjectID `bson:"actor_id" json:"actor_id"`
	At      time.Time          `bson:"at" json:"at"`
}

type TripReconciliationIssue struct {
	ID                primitive.ObjectID     `bson:"_id,omitempty" json:"id"`
	IssueKey          string                 `bson:"issue_key" json:"issue_key"`
	OfficeID          primitive.ObjectID     `bson:"office_id" json:"office_id"`
	IssueType         string                 `bson:"issue_type" json:"issue_type"`
	Severity          string                 `bson:"severity" json:"severity"`
	Status            string                 `bson:"status" json:"status"`
	TripID            primitive.ObjectID     `bson:"trip_id" json:"trip_id"`
	TripNumber        string                 `bson:"trip_number" json:"trip_number"`
	ServiceDate       string                 `bson:"service_date" json:"service_date"`
	WorkerID          *primitive.ObjectID    `bson:"worker_id,omitempty" json:"worker_id,omitempty"`
	WorkerType        string                 `bson:"worker_type,omitempty" json:"worker_type,omitempty"`
	WorkerName        string                 `bson:"worker_name,omitempty" json:"worker_name,omitempty"`
	EmployeeCode      string                 `bson:"employee_code,omitempty" json:"employee_code,omitempty"`
	ExpectedPaise     int64                  `bson:"expected_paise" json:"expected_paise"`
	ActualPaise       int64                  `bson:"actual_paise" json:"actual_paise"`
	DifferencePaise   int64                  `bson:"difference_paise" json:"difference_paise"`
	Explanation       string                 `bson:"explanation" json:"explanation"`
	RecommendedFix    string                 `bson:"recommended_fix" json:"recommended_fix"`
	AutoFixable       bool                   `bson:"auto_fixable" json:"auto_fixable"`
	Details           map[string]interface{} `bson:"details,omitempty" json:"details,omitempty"`
	FirstDetectedAt   time.Time              `bson:"first_detected_at" json:"first_detected_at"`
	LastDetectedAt    time.Time              `bson:"last_detected_at" json:"last_detected_at"`
	ResolvedAt        *time.Time             `bson:"resolved_at,omitempty" json:"resolved_at,omitempty"`
	ResolvedBy        *primitive.ObjectID    `bson:"resolved_by,omitempty" json:"resolved_by,omitempty"`
	Resolution        *TripIssueResolution   `bson:"resolution,omitempty" json:"resolution,omitempty"`
	ResolutionHistory []TripIssueResolution  `bson:"resolution_history,omitempty" json:"resolution_history,omitempty"`
}

type TripIssueFilter struct {
	OfficeID  primitive.ObjectID
	StartDate string
	EndDate   string
	Status    string
	IssueType string
	Severity  string
	Search    string
	Page      int64
	Limit     int64
}

type TripIssueScanResult struct {
	Scanned       int64 `json:"scanned"`
	Open          int64 `json:"open"`
	Created       int64 `json:"created"`
	Updated       int64 `json:"updated"`
	AutoResolved  int64 `json:"auto_resolved"`
	TotalVariance int64 `json:"total_variance_paise"`
}

type TripIssueActionInput struct {
	Action string
	Reason string
	Actor  primitive.ObjectID
}

type TripIssueStore interface {
	ScanTripIssues(context.Context, primitive.ObjectID, string, string) (TripIssueScanResult, error)
	ListTripIssues(context.Context, TripIssueFilter) ([]TripReconciliationIssue, int64, error)
	ActOnTripIssue(context.Context, primitive.ObjectID, primitive.ObjectID, TripIssueActionInput) (TripReconciliationIssue, error)
}

var (
	ErrTripIssueNotFound      = errors.New("trip reconciliation issue was not found")
	ErrTripIssueAlreadyClosed = errors.New("trip reconciliation issue is already closed")
	ErrTripIssueUnsupported   = errors.New("this trip reconciliation action is not supported for the issue")
	ErrTripIssuePaid          = errors.New("paid trip snapshots are immutable; post an audited adjustment instead")
)

type tripIssueWorker struct {
	ID        primitive.ObjectID `bson:"_id"`
	FirstName string             `bson:"first_name"`
	LastName  string             `bson:"last_name"`
	Name      string             `bson:"name"`
	EmpCode   string             `bson:"emp_code"`
	IsDeleted bool               `bson:"is_deleted"`
}

type tripIssueCalculation struct {
	Distance   float64
	Commission float64
	Petrol     float64
	Cost       float64
	Mileage    float64
	Valid      bool
}

func allowanceWorker(trip TripSource) (*primitive.ObjectID, string) {
	if trip.DriverBeauticianID != nil {
		return trip.DriverBeauticianID, "beautician"
	}
	if trip.IsSelfDrive && trip.BeauticianID != nil {
		return trip.BeauticianID, "beautician"
	}
	return trip.RiderID, "rider"
}

func canonicalTripCalculation(trip TripSource) tripIssueCalculation {
	distance := trip.FareCalculation.TripDistanceKM
	if !trip.IsManualDistance && trip.AutoDistanceKM > 0 {
		distance = trip.AutoDistanceKM + trip.ExtraKM
		if trip.IsTwoWay {
			distance = trip.AutoDistanceKM*2 + trip.ExtraKM
		}
	}
	if !finiteNonNegative(distance) || distance <= 0 {
		return tripIssueCalculation{}
	}
	rate := 1.0
	if trip.Snapshot != nil && trip.Snapshot.CommissionRatePerKM != nil && *trip.Snapshot.CommissionRatePerKM > 0 {
		rate = *trip.Snapshot.CommissionRatePerKM
	}
	commission := 0.0
	if trip.IsCommissionable {
		commission = roundSourceMoney(distance * rate)
		if trip.CommissionAmount > 0 {
			commission = roundSourceMoney(trip.CommissionAmount)
		}
	}
	cost, mileage := trip.FareCalculation.PetrolCostPerLiter, trip.FareCalculation.StandardMileagePerLiter
	if trip.Snapshot != nil {
		if trip.Snapshot.PetrolCostPerLiter != nil && *trip.Snapshot.PetrolCostPerLiter > 0 {
			cost = *trip.Snapshot.PetrolCostPerLiter
		}
		if trip.Snapshot.StandardMileagePerLiter != nil && *trip.Snapshot.StandardMileagePerLiter > 0 {
			mileage = *trip.Snapshot.StandardMileagePerLiter
		}
	}
	if cost <= 0 {
		cost = trip.OfficePetrolCostPerLiter
	}
	if mileage <= 0 {
		mileage = trip.OfficeStandardMileagePerLiter
	}
	if !finiteNonNegative(cost) || !finiteNonNegative(mileage) || cost <= 0 || mileage <= 0 {
		return tripIssueCalculation{Distance: distance, Commission: commission, Cost: cost, Mileage: mileage}
	}
	return tripIssueCalculation{
		Distance: distance, Commission: commission, Petrol: roundSourceMoney(distance / mileage * cost),
		Cost: cost, Mileage: mileage, Valid: true,
	}
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func tripIssueSeverityForMoney(difference int64) string {
	return orderIssueSeverity(difference)
}

func workerDisplayName(worker tripIssueWorker) string {
	if value := strings.TrimSpace(worker.Name); value != "" {
		return value
	}
	return strings.TrimSpace(worker.FirstName + " " + worker.LastName)
}

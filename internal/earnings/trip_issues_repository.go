package earnings

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/payables"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (r *Repository) ScanTripIssues(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) (TripIssueScanResult, error) {
	result := TripIssueScanResult{}
	filter := payables.TripBaseMatch(startDate, endDate)
	filter["office_id"] = officeID
	cursor, err := r.db.Collection("trips").Find(ctx, filter)
	if err != nil {
		return result, err
	}
	defer cursor.Close(ctx)

	cost, mileage, err := r.loadOfficeTripRates(ctx, officeID)
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	detectedKeys := make([]string, 0)
	for cursor.Next(ctx) {
		var trip TripSource
		if err := cursor.Decode(&trip); err != nil {
			return result, err
		}
		trip.OfficePetrolCostPerLiter = cost
		trip.OfficeStandardMileagePerLiter = mileage
		result.Scanned++
		issues, err := r.detectTripIssues(ctx, trip, now)
		if err != nil {
			return result, err
		}
		for _, issue := range issues {
			detectedKeys = append(detectedKeys, issue.IssueKey)
			stored, created, err := r.upsertTripIssue(ctx, issue)
			if err != nil {
				return result, err
			}
			if created {
				result.Created++
			} else {
				result.Updated++
			}
			if stored.Status == OrderIssueOpen {
				result.Open++
				result.TotalVariance += stored.DifferencePaise
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return result, err
	}

	staleFilter := bson.M{
		"office_id": officeID, "service_date": bson.M{"$gte": startDate, "$lte": endDate},
		"status": OrderIssueOpen,
	}
	if len(detectedKeys) > 0 {
		staleFilter["issue_key"] = bson.M{"$nin": detectedKeys}
	}
	resolution := TripIssueResolution{Action: "auto_recheck", Reason: "The discrepancy was no longer present during a scan.", At: now}
	updateResult, err := r.db.Collection(tripIssueCollection).UpdateMany(ctx, staleFilter, bson.M{
		"$set":  bson.M{"status": OrderIssueResolved, "resolved_at": now, "resolution": resolution, "last_detected_at": now},
		"$push": bson.M{"resolution_history": resolution},
	})
	if err != nil {
		return result, err
	}
	result.AutoResolved = updateResult.ModifiedCount
	return result, nil
}

func (r *Repository) detectTripIssues(ctx context.Context, trip TripSource, now time.Time) ([]TripReconciliationIssue, error) {
	issues := make([]TripReconciliationIssue, 0)
	workerID, workerType := allowanceWorker(trip)
	var worker tripIssueWorker
	workerValid := false
	if workerID == nil || workerID.IsZero() {
		issues = append(issues, newTripIssue(trip, nil, workerType, tripIssueWorker{}, TripIssueMissingWorker, "critical", 0, 0,
			"The completed trip has no payable worker assignment.",
			"Assign the correct rider or driving beautician, then recheck the trip.", false, now, nil))
	} else {
		collection := "riders"
		if workerType == "beautician" {
			collection = "beauticians"
		}
		err := r.db.Collection(collection).FindOne(ctx, bson.M{"_id": *workerID, "office_id": trip.OfficeID}).Decode(&worker)
		if errors.Is(err, mongo.ErrNoDocuments) {
			issues = append(issues, newTripIssue(trip, workerID, workerType, tripIssueWorker{}, TripIssueMissingWorker, "critical", 0, 0,
				"The payable worker profile does not exist in this office.",
				"Open the trip, assign a valid worker from this office, then recheck.", false, now, nil))
		} else if err != nil {
			return nil, err
		} else if worker.IsDeleted {
			details := map[string]interface{}{"assigned_profile_deleted": true}
			if candidate, candidateType, found, err := r.activeWorkerWithEmployeeCode(ctx, trip.OfficeID, worker.EmpCode, worker.ID); err != nil {
				return nil, err
			} else if found {
				details["candidate_worker_id"] = candidate.ID.Hex()
				details["candidate_worker_type"] = candidateType
				details["candidate_worker_name"] = workerDisplayName(candidate)
				details["candidate_employee_code"] = candidate.EmpCode
			}
			issues = append(issues, newTripIssue(trip, workerID, workerType, worker, TripIssueDeletedWorker, "critical", 0, 0,
				"The trip is assigned to a deleted worker profile.",
				"Review the suggested active profile, reassign the trip at source, and recheck. Identity changes are never applied automatically.", false, now, details))
		} else {
			workerValid = true
		}
	}

	calculation := canonicalTripCalculation(trip)
	if !calculation.Valid {
		details := map[string]interface{}{
			"expected_distance_km": calculation.Distance, "petrol_cost_per_liter": calculation.Cost,
			"standard_mileage_per_liter": calculation.Mileage,
		}
		issues = append(issues, newTripIssue(trip, workerID, workerType, worker, TripIssueInvalidConfig, "critical", 0, 0,
			"Payable distance or office petrol settings are missing or invalid.",
			"Correct the trip distance and office petrol/mileage settings, then rebuild the payable snapshot.", false, now, details))
		return issues, nil
	}

	expectedCommission := moneyToPaise(calculation.Commission)
	expectedPetrol := moneyToPaise(calculation.Petrol)
	if trip.Snapshot == nil {
		issues = append(issues, newTripIssue(trip, workerID, workerType, worker, TripIssueMissingSnapshot, "critical",
			expectedCommission+expectedPetrol, 0,
			"The completed trip has no payable snapshot, so commission and petrol cannot be audited or frozen.",
			"Build a canonical payable snapshot from the verified distance and office rates.", workerValid, now,
			map[string]interface{}{"expected_distance_km": calculation.Distance, "expected_commission_paise": expectedCommission, "expected_petrol_paise": expectedPetrol}))
		return issues, nil
	}

	paid := trip.Snapshot.IsPaid
	autoFixable := !paid && workerValid
	safety := "Rebuild the unpaid payable snapshot from the canonical trip inputs."
	if paid {
		safety = "This snapshot is already paid. Verify it and post an audited earnings adjustment; do not rewrite the source snapshot."
	}
	if trip.Snapshot.PayableDistanceKM == nil || math.Abs(*trip.Snapshot.PayableDistanceKM-calculation.Distance) > 0.001 {
		actualDistance := 0.0
		if trip.Snapshot.PayableDistanceKM != nil {
			actualDistance = *trip.Snapshot.PayableDistanceKM
		}
		issues = append(issues, newTripIssue(trip, workerID, workerType, worker, TripIssueDistanceMismatch, "high", 0, 0,
			"The stored payable distance does not match the canonical distance inputs.", safety, autoFixable, now,
			map[string]interface{}{"expected_distance_km": calculation.Distance, "actual_distance_km": actualDistance, "snapshot_paid": paid}))
	}
	actualPetrol := int64(0)
	if trip.Snapshot.PetrolPayable != nil && finiteNonNegative(*trip.Snapshot.PetrolPayable) {
		actualPetrol = moneyToPaise(*trip.Snapshot.PetrolPayable)
	}
	if trip.Snapshot.PetrolPayable == nil || actualPetrol != expectedPetrol {
		issues = append(issues, newTripIssue(trip, workerID, workerType, worker, TripIssuePetrolMismatch,
			tripIssueSeverityForMoney(actualPetrol-expectedPetrol), expectedPetrol, actualPetrol,
			"Stored petrol payable does not match distance ÷ mileage × petrol cost.", safety, autoFixable, now,
			map[string]interface{}{"distance_km": calculation.Distance, "petrol_cost_per_liter": calculation.Cost, "standard_mileage_per_liter": calculation.Mileage, "snapshot_paid": paid}))
	}
	actualCommission := int64(0)
	if trip.Snapshot.CommissionPayable != nil && finiteNonNegative(*trip.Snapshot.CommissionPayable) {
		actualCommission = moneyToPaise(*trip.Snapshot.CommissionPayable)
	}
	if trip.Snapshot.CommissionPayable == nil || actualCommission != expectedCommission {
		issues = append(issues, newTripIssue(trip, workerID, workerType, worker, TripIssueCommissionMismatch,
			tripIssueSeverityForMoney(actualCommission-expectedCommission), expectedCommission, actualCommission,
			"Stored trip commission does not match the canonical commission rule.", safety, autoFixable, now,
			map[string]interface{}{"distance_km": calculation.Distance, "commission_applicable": trip.IsCommissionable, "commission_override": trip.CommissionAmount, "snapshot_paid": paid}))
	}
	return issues, nil
}

func (r *Repository) activeWorkerWithEmployeeCode(ctx context.Context, officeID primitive.ObjectID, empCode string, excludeID primitive.ObjectID) (tripIssueWorker, string, bool, error) {
	if strings.TrimSpace(empCode) == "" {
		return tripIssueWorker{}, "", false, nil
	}
	for _, item := range []struct{ collection, workerType string }{{"riders", "rider"}, {"beauticians", "beautician"}} {
		var worker tripIssueWorker
		err := r.db.Collection(item.collection).FindOne(ctx, bson.M{
			"office_id": officeID, "emp_code": empCode, "_id": bson.M{"$ne": excludeID}, "is_deleted": bson.M{"$ne": true},
		}).Decode(&worker)
		if err == nil {
			return worker, item.workerType, true, nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return tripIssueWorker{}, "", false, err
		}
	}
	return tripIssueWorker{}, "", false, nil
}

func newTripIssue(trip TripSource, workerID *primitive.ObjectID, workerType string, worker tripIssueWorker, issueType, severity string,
	expected, actual int64, explanation, fix string, autoFixable bool, now time.Time, details map[string]interface{}) TripReconciliationIssue {
	return TripReconciliationIssue{
		IssueKey: trip.OfficeID.Hex() + ":" + trip.ID.Hex() + ":" + issueType,
		OfficeID: trip.OfficeID, IssueType: issueType, Severity: severity, Status: OrderIssueOpen,
		TripID: trip.ID, TripNumber: trip.TripNumber, ServiceDate: trip.Date,
		WorkerID: workerID, WorkerType: workerType, WorkerName: workerDisplayName(worker), EmployeeCode: worker.EmpCode,
		ExpectedPaise: expected, ActualPaise: actual, DifferencePaise: actual - expected,
		Explanation: explanation, RecommendedFix: fix, AutoFixable: autoFixable, Details: details,
		FirstDetectedAt: now, LastDetectedAt: now, ResolutionHistory: []TripIssueResolution{},
	}
}

func (r *Repository) upsertTripIssue(ctx context.Context, issue TripReconciliationIssue) (TripReconciliationIssue, bool, error) {
	var existing TripReconciliationIssue
	err := r.db.Collection(tripIssueCollection).FindOne(ctx, bson.M{"issue_key": issue.IssueKey}).Decode(&existing)
	if errors.Is(err, mongo.ErrNoDocuments) {
		issue.ID = primitive.NewObjectID()
		_, err = r.db.Collection(tripIssueCollection).InsertOne(ctx, issue)
		if mongo.IsDuplicateKeyError(err) {
			return r.upsertTripIssue(ctx, issue)
		}
		return issue, true, err
	}
	if err != nil {
		return TripReconciliationIssue{}, false, err
	}
	reopen := existing.Status == OrderIssueResolved || (existing.Status == OrderIssueAccepted && existing.DifferencePaise != issue.DifferencePaise)
	set := bson.M{
		"issue_type": issue.IssueType, "severity": issue.Severity, "trip_number": issue.TripNumber,
		"service_date": issue.ServiceDate, "worker_id": issue.WorkerID, "worker_type": issue.WorkerType,
		"worker_name": issue.WorkerName, "employee_code": issue.EmployeeCode,
		"expected_paise": issue.ExpectedPaise, "actual_paise": issue.ActualPaise, "difference_paise": issue.DifferencePaise,
		"explanation": issue.Explanation, "recommended_fix": issue.RecommendedFix, "auto_fixable": issue.AutoFixable,
		"details": issue.Details, "last_detected_at": issue.LastDetectedAt,
	}
	update := bson.M{"$set": set}
	if reopen {
		set["status"] = OrderIssueOpen
		update["$unset"] = bson.M{"resolved_at": "", "resolved_by": "", "resolution": ""}
	}
	result := r.db.Collection(tripIssueCollection).FindOneAndUpdate(ctx, bson.M{"_id": existing.ID}, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After))
	var stored TripReconciliationIssue
	if err := result.Decode(&stored); err != nil {
		return TripReconciliationIssue{}, false, err
	}
	return stored, false, nil
}

func (r *Repository) ListTripIssues(ctx context.Context, input TripIssueFilter) ([]TripReconciliationIssue, int64, error) {
	filter := bson.M{"office_id": input.OfficeID}
	if input.StartDate != "" {
		filter["service_date"] = bson.M{"$gte": input.StartDate, "$lte": input.EndDate}
	}
	if input.Status != "" && input.Status != "all" {
		filter["status"] = input.Status
	}
	if input.IssueType != "" && input.IssueType != "all" {
		filter["issue_type"] = input.IssueType
	}
	if input.Severity != "" && input.Severity != "all" {
		filter["severity"] = input.Severity
	}
	if search := strings.TrimSpace(input.Search); search != "" {
		pattern := primitive.Regex{Pattern: regexpQuote(search), Options: "i"}
		filter["$or"] = bson.A{bson.M{"trip_number": pattern}, bson.M{"worker_name": pattern}, bson.M{"employee_code": pattern}}
	}
	total, err := r.db.Collection(tripIssueCollection).CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := r.db.Collection(tripIssueCollection).Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "status", Value: 1}, {Key: "severity", Value: 1}, {Key: "last_detected_at", Value: -1}}).
		SetSkip((input.Page-1)*input.Limit).SetLimit(input.Limit))
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	issues := make([]TripReconciliationIssue, 0)
	if err := cursor.All(ctx, &issues); err != nil {
		return nil, 0, err
	}
	return issues, total, nil
}

func (r *Repository) ActOnTripIssue(ctx context.Context, officeID, issueID primitive.ObjectID, input TripIssueActionInput) (TripReconciliationIssue, error) {
	var issue TripReconciliationIssue
	if err := r.db.Collection(tripIssueCollection).FindOne(ctx, bson.M{"_id": issueID, "office_id": officeID}).Decode(&issue); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return TripReconciliationIssue{}, ErrTripIssueNotFound
		}
		return TripReconciliationIssue{}, err
	}
	if issue.Status != OrderIssueOpen && input.Action != TripIssueActionRecheck {
		return TripReconciliationIssue{}, ErrTripIssueAlreadyClosed
	}
	switch input.Action {
	case TripIssueActionRecheck:
		return r.recheckTripIssue(ctx, issue, input)
	case TripIssueActionAccept:
		return r.closeTripIssue(ctx, issue, OrderIssueAccepted, input)
	case TripIssueActionRebuild:
		if !issue.AutoFixable {
			return TripReconciliationIssue{}, ErrTripIssueUnsupported
		}
		return r.rebuildTripSnapshot(ctx, issue, input)
	default:
		return TripReconciliationIssue{}, ErrTripIssueUnsupported
	}
}

func (r *Repository) loadTripForIssue(ctx context.Context, tripID, officeID primitive.ObjectID) (TripSource, error) {
	var trip TripSource
	err := r.db.Collection("trips").FindOne(ctx, bson.M{"_id": tripID, "office_id": officeID}).Decode(&trip)
	if err != nil {
		return trip, err
	}
	trip.OfficePetrolCostPerLiter, trip.OfficeStandardMileagePerLiter, err = r.loadOfficeTripRates(ctx, officeID)
	return trip, err
}

func (r *Repository) recheckTripIssue(ctx context.Context, issue TripReconciliationIssue, input TripIssueActionInput) (TripReconciliationIssue, error) {
	trip, err := r.loadTripForIssue(ctx, issue.TripID, issue.OfficeID)
	if err != nil {
		return TripReconciliationIssue{}, err
	}
	detected, err := r.detectTripIssues(ctx, trip, time.Now().UTC())
	if err != nil {
		return TripReconciliationIssue{}, err
	}
	for _, candidate := range detected {
		if candidate.IssueType == issue.IssueType {
			stored, _, err := r.upsertTripIssue(ctx, candidate)
			return stored, err
		}
	}
	action, reason := input.Action, input.Reason
	if action == "" {
		action = TripIssueActionRecheck
	}
	if reason == "" {
		reason = "The source trip now reconciles."
	}
	return r.closeTripIssue(ctx, issue, OrderIssueResolved, TripIssueActionInput{Action: action, Reason: reason, Actor: input.Actor})
}

func (r *Repository) rebuildTripSnapshot(ctx context.Context, issue TripReconciliationIssue, input TripIssueActionInput) (TripReconciliationIssue, error) {
	trip, err := r.loadTripForIssue(ctx, issue.TripID, issue.OfficeID)
	if err != nil {
		return TripReconciliationIssue{}, err
	}
	if trip.Snapshot != nil && trip.Snapshot.IsPaid {
		return TripReconciliationIssue{}, ErrTripIssuePaid
	}
	workerID, workerType := allowanceWorker(trip)
	if workerID == nil || workerID.IsZero() {
		return TripReconciliationIssue{}, fmt.Errorf("%w: assign an active worker first", ErrTripIssueUnsupported)
	}
	collection := "riders"
	if workerType == "beautician" {
		collection = "beauticians"
	}
	activeWorkers, err := r.db.Collection(collection).CountDocuments(ctx, bson.M{
		"_id": *workerID, "office_id": trip.OfficeID, "is_deleted": bson.M{"$ne": true},
	})
	if err != nil {
		return TripReconciliationIssue{}, err
	}
	if activeWorkers != 1 {
		return TripReconciliationIssue{}, fmt.Errorf("%w: assign an active worker first", ErrTripIssueUnsupported)
	}
	calculation := canonicalTripCalculation(trip)
	if !calculation.Valid {
		return TripReconciliationIssue{}, fmt.Errorf("%w: correct distance and office rates first", ErrTripIssueUnsupported)
	}
	now := time.Now().UTC()
	rate := 1.0
	if trip.Snapshot != nil && trip.Snapshot.CommissionRatePerKM != nil && *trip.Snapshot.CommissionRatePerKM > 0 {
		rate = *trip.Snapshot.CommissionRatePerKM
	}
	snapshot := bson.M{
		"office_id": trip.OfficeID, "source": "office", "captured_at": now,
		"petrol_cost_per_liter": calculation.Cost, "standard_mileage_per_liter": calculation.Mileage,
		"rider_commission_rule": "per_km_or_override", "rider_commission_rate_per_km": rate,
		"payable_distance_km": calculation.Distance, "commission_payable": calculation.Commission,
		"petrol_payable": calculation.Petrol, "is_paid": false, "reconciliation_issue_id": issue.ID,
		"reconciled_by": input.Actor, "reconciliation_reason": input.Reason,
	}
	update := bson.M{"$set": bson.M{"payable_snapshot": snapshot}}
	result, err := r.db.Collection("trips").UpdateOne(ctx, bson.M{
		"_id": trip.ID, "office_id": trip.OfficeID, "payable_snapshot.is_paid": bson.M{"$ne": true},
	}, update)
	if err != nil {
		return TripReconciliationIssue{}, err
	}
	if result.MatchedCount != 1 {
		return TripReconciliationIssue{}, ErrTripIssuePaid
	}
	return r.recheckTripIssue(ctx, issue, TripIssueActionInput{Action: TripIssueActionRebuild, Reason: input.Reason, Actor: input.Actor})
}

func (r *Repository) closeTripIssue(ctx context.Context, issue TripReconciliationIssue, status string, input TripIssueActionInput) (TripReconciliationIssue, error) {
	now := time.Now().UTC()
	resolution := TripIssueResolution{Action: input.Action, Reason: input.Reason, ActorID: input.Actor, At: now}
	result := r.db.Collection(tripIssueCollection).FindOneAndUpdate(ctx,
		bson.M{"_id": issue.ID, "office_id": issue.OfficeID},
		bson.M{"$set": bson.M{"status": status, "resolved_at": now, "resolved_by": input.Actor, "resolution": resolution, "last_detected_at": now}, "$push": bson.M{"resolution_history": resolution}},
		options.FindOneAndUpdate().SetReturnDocument(options.After))
	var stored TripReconciliationIssue
	if err := result.Decode(&stored); err != nil {
		return TripReconciliationIssue{}, err
	}
	return stored, nil
}

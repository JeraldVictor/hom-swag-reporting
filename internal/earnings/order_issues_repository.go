package earnings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (r *Repository) ScanOrderIssues(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) (OrderIssueScanResult, error) {
	result := OrderIssueScanResult{}
	filter := bson.M{
		"office_id": officeID, "status": "completed", "is_deleted": bson.M{"$ne": true},
		"booking_info.date": bson.M{"$gte": startDate, "$lte": endDate},
	}
	cursor, err := r.db.Collection("orders").Find(ctx, filter)
	if err != nil {
		return result, err
	}
	defer cursor.Close(ctx)

	now := time.Now().UTC()
	detectedKeys := make([]string, 0)
	for cursor.Next(ctx) {
		var source orderIssueSource
		if err := cursor.Decode(&source); err != nil {
			return result, err
		}
		result.Scanned++
		if !validOrderIssueSource(source) {
			continue
		}
		detected := detectOrderIssues(source, now)
		var beautician *tripIssueWorker
		if source.BeauticianID != nil && !source.BeauticianID.IsZero() {
			var row tripIssueWorker
			err := r.db.Collection("beauticians").FindOne(ctx, bson.M{"_id": *source.BeauticianID, "office_id": officeID}).Decode(&row)
			if err == nil {
				beautician = &row
			} else if !errors.Is(err, mongo.ErrNoDocuments) {
				return result, err
			}
		}
		detected = append(detected, detectOrderCommissionIssues(source, beautician, now)...)
		for _, issue := range detected {
			detectedKeys = append(detectedKeys, issue.IssueKey)
			stored, created, err := r.upsertOrderIssue(ctx, issue)
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
	autoResolution := OrderIssueResolution{Action: "auto_recheck", Reason: "The discrepancy was no longer present during a scan.", At: now}
	updateResult, err := r.db.Collection(orderIssueCollection).UpdateMany(ctx, staleFilter, bson.M{
		"$set":  bson.M{"status": OrderIssueResolved, "resolved_at": now, "resolution": autoResolution, "last_detected_at": now},
		"$push": bson.M{"resolution_history": autoResolution},
	})
	if err != nil {
		return result, err
	}
	result.AutoResolved = updateResult.ModifiedCount
	return result, nil
}

func (r *Repository) upsertOrderIssue(ctx context.Context, issue OrderReconciliationIssue) (OrderReconciliationIssue, bool, error) {
	var existing OrderReconciliationIssue
	err := r.db.Collection(orderIssueCollection).FindOne(ctx, bson.M{"issue_key": issue.IssueKey}).Decode(&existing)
	if errors.Is(err, mongo.ErrNoDocuments) {
		issue.ID = primitive.NewObjectID()
		_, err = r.db.Collection(orderIssueCollection).InsertOne(ctx, issue)
		if mongo.IsDuplicateKeyError(err) {
			return r.upsertOrderIssue(ctx, issue)
		}
		return issue, true, err
	}
	if err != nil {
		return OrderReconciliationIssue{}, false, err
	}

	status := existing.Status
	reopen := status == OrderIssueResolved || (status == OrderIssueAccepted && existing.DifferencePaise != issue.DifferencePaise)
	set := bson.M{
		"issue_type": issue.IssueType, "severity": issue.Severity, "order_number": issue.OrderNumber,
		"service_date": issue.ServiceDate, "expected_paise": issue.ExpectedPaise, "actual_paise": issue.ActualPaise,
		"difference_paise": issue.DifferencePaise, "payment_method": issue.PaymentMethod,
		"explanation": issue.Explanation, "details": issue.Details, "last_detected_at": issue.LastDetectedAt,
	}
	update := bson.M{"$set": set}
	if reopen {
		set["status"] = OrderIssueOpen
		update["$unset"] = bson.M{"resolved_at": "", "resolved_by": "", "resolution": ""}
	}
	result := r.db.Collection(orderIssueCollection).FindOneAndUpdate(ctx, bson.M{"_id": existing.ID}, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After))
	var stored OrderReconciliationIssue
	if err := result.Decode(&stored); err != nil {
		return OrderReconciliationIssue{}, false, err
	}
	return stored, false, nil
}

func (r *Repository) ListOrderIssues(ctx context.Context, input OrderIssueFilter) ([]OrderReconciliationIssue, int64, error) {
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
		filter["order_number"] = primitive.Regex{Pattern: regexpQuote(search), Options: "i"}
	}
	total, err := r.db.Collection(orderIssueCollection).CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := r.db.Collection(orderIssueCollection).Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "status", Value: 1}, {Key: "severity", Value: 1}, {Key: "last_detected_at", Value: -1}}).
		SetSkip((input.Page-1)*input.Limit).SetLimit(input.Limit))
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	issues := make([]OrderReconciliationIssue, 0)
	if err := cursor.All(ctx, &issues); err != nil {
		return nil, 0, err
	}
	return issues, total, nil
}

func (r *Repository) ActOnOrderIssue(ctx context.Context, officeID, issueID primitive.ObjectID, input OrderIssueActionInput) (OrderReconciliationIssue, error) {
	var issue OrderReconciliationIssue
	if err := r.db.Collection(orderIssueCollection).FindOne(ctx, bson.M{"_id": issueID, "office_id": officeID}).Decode(&issue); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return OrderReconciliationIssue{}, ErrOrderIssueNotFound
		}
		return OrderReconciliationIssue{}, err
	}
	if issue.Status != OrderIssueOpen && input.Action != OrderIssueActionRecheck {
		return OrderReconciliationIssue{}, ErrOrderIssueAlreadyClosed
	}

	switch input.Action {
	case OrderIssueActionRecheck:
		return r.recheckOrderIssue(ctx, issue, input)
	case OrderIssueActionAccept:
		return r.closeOrderIssue(ctx, issue, OrderIssueAccepted, input, 0)
	case OrderIssueActionAlign:
		if issue.IssueType != OrderIssuePaymentMismatch && issue.IssueType != OrderIssueMissingPayment {
			return OrderReconciliationIssue{}, ErrOrderIssueUnsupported
		}
		return r.alignOrderPayment(ctx, issue, input)
	default:
		return OrderReconciliationIssue{}, ErrOrderIssueUnsupported
	}
}

func (r *Repository) loadOrderIssueSource(ctx context.Context, orderID, officeID primitive.ObjectID) (orderIssueSource, error) {
	var source orderIssueSource
	err := r.db.Collection("orders").FindOne(ctx, bson.M{"_id": orderID, "office_id": officeID}).Decode(&source)
	return source, err
}

func (r *Repository) recheckOrderIssue(ctx context.Context, issue OrderReconciliationIssue, input OrderIssueActionInput) (OrderReconciliationIssue, error) {
	source, err := r.loadOrderIssueSource(ctx, issue.OrderID, issue.OfficeID)
	if err != nil {
		return OrderReconciliationIssue{}, err
	}
	now := time.Now().UTC()
	detectedIssues := detectOrderIssues(source, now)
	var beautician *tripIssueWorker
	if source.BeauticianID != nil && !source.BeauticianID.IsZero() {
		var row tripIssueWorker
		err := r.db.Collection("beauticians").FindOne(ctx, bson.M{"_id": *source.BeauticianID, "office_id": source.OfficeID}).Decode(&row)
		if err == nil {
			beautician = &row
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return OrderReconciliationIssue{}, err
		}
	}
	detectedIssues = append(detectedIssues, detectOrderCommissionIssues(source, beautician, now)...)
	for _, detected := range detectedIssues {
		if detected.IssueType == issue.IssueType {
			stored, _, err := r.upsertOrderIssue(ctx, detected)
			return stored, err
		}
	}
	return r.closeOrderIssue(ctx, issue, OrderIssueResolved, OrderIssueActionInput{
		Action: OrderIssueActionRecheck, Reason: "The source order now reconciles.", Actor: input.Actor,
	}, 0)
}

func (r *Repository) alignOrderPayment(ctx context.Context, issue OrderReconciliationIssue, input OrderIssueActionInput) (OrderReconciliationIssue, error) {
	source, err := r.loadOrderIssueSource(ctx, issue.OrderID, issue.OfficeID)
	if err != nil {
		return OrderReconciliationIssue{}, err
	}
	if len(source.Payment.History) == 0 {
		return OrderReconciliationIssue{}, fmt.Errorf("%w: legacy payments without history must be corrected at source", ErrOrderIssueUnsupported)
	}
	now := time.Now().UTC()
	var current *OrderReconciliationIssue
	for _, detected := range detectOrderIssues(source, now) {
		if detected.IssueType == issue.IssueType {
			copy := detected
			current = &copy
			break
		}
	}
	if current == nil {
		return r.closeOrderIssue(ctx, issue, OrderIssueResolved, OrderIssueActionInput{Action: OrderIssueActionRecheck, Reason: "The source order now reconciles.", Actor: input.Actor}, 0)
	}
	adjustmentPaise := -current.DifferencePaise
	method := normalizeOrderPaymentText(source.Payment.Method)
	if method == "" {
		method = "other"
	}
	entry := bson.M{
		"_id": primitive.NewObjectID(), "label": "Reconciliation adjustment", "method": method,
		"amount": float64(adjustmentPaise) / 100, "status": "adjusted", "recorded_at": now,
		"recorded_by": input.Actor, "reason": input.Reason, "reconciliation_issue_id": issue.ID,
	}
	updateResult, err := r.db.Collection("orders").UpdateOne(ctx, bson.M{
		"_id": issue.OrderID, "office_id": issue.OfficeID,
		"payment.history": bson.M{"$not": bson.M{"$elemMatch": bson.M{"reconciliation_issue_id": issue.ID}}},
	}, bson.M{"$push": bson.M{"payment.history": entry}, "$set": bson.M{"updated_at": now}})
	if err != nil {
		return OrderReconciliationIssue{}, err
	}
	if updateResult.ModifiedCount == 0 {
		var alreadyApplied int64
		alreadyApplied, err = r.db.Collection("orders").CountDocuments(ctx, bson.M{
			"_id": issue.OrderID, "office_id": issue.OfficeID,
			"payment.history": bson.M{"$elemMatch": bson.M{"reconciliation_issue_id": issue.ID}},
		})
		if err != nil {
			return OrderReconciliationIssue{}, err
		}
		if alreadyApplied == 0 {
			return OrderReconciliationIssue{}, ErrOrderIssueStillPresent
		}
	}
	return r.closeOrderIssue(ctx, issue, OrderIssueResolved, input, adjustmentPaise)
}

func (r *Repository) closeOrderIssue(ctx context.Context, issue OrderReconciliationIssue, status string, input OrderIssueActionInput, adjustmentPaise int64) (OrderReconciliationIssue, error) {
	now := time.Now().UTC()
	resolution := OrderIssueResolution{Action: input.Action, Reason: input.Reason, ActorID: input.Actor, At: now, AdjustmentPaise: adjustmentPaise}
	result := r.db.Collection(orderIssueCollection).FindOneAndUpdate(ctx,
		bson.M{"_id": issue.ID, "office_id": issue.OfficeID},
		bson.M{"$set": bson.M{"status": status, "resolved_at": now, "resolved_by": input.Actor, "resolution": resolution, "last_detected_at": now}, "$push": bson.M{"resolution_history": resolution}},
		options.FindOneAndUpdate().SetReturnDocument(options.After))
	var stored OrderReconciliationIssue
	if err := result.Decode(&stored); err != nil {
		return OrderReconciliationIssue{}, err
	}
	return stored, nil
}

func regexpQuote(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", ".", "\\.", "+", "\\+", "*", "\\*", "?", "\\?", "(", "\\(", ")", "\\)", "[", "\\[", "]", "\\]", "{", "\\{", "}", "\\}", "^", "\\^", "$", "\\$", "|", "\\|")
	return replacer.Replace(value)
}

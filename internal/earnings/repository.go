package earnings

import (
	"context"
	"errors"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/payables"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	ledgerCollection     = "earnings_ledger"
	periodCollection     = "earnings_periods"
	rebuildCollection    = "earnings_rebuild_jobs"
	settlementCollection = "earnings_settlements"
	modeCollection       = "earnings_mode_settings"
	orderIssueCollection = "earnings_order_issues"
)

var (
	ErrSettlementExceedsPending     = errors.New("settlement amount exceeds the worker's pending earnings for this range")
	ErrNoPendingEarnings            = errors.New("no pending earnings exist for this worker, bucket, and range")
	ErrSettlementEntriesUnavailable = errors.New("one or more selected earnings are already paid or unavailable")
	ErrSettlementNotFound           = errors.New("settlement not found")
)

type Repository struct {
	db                          *mongo.Database
	defaultMode                 string
	allowNonTransactionalWrites bool
	startSession                func(...*options.SessionOptions) (mongo.Session, error)
}

type RepositoryOptions struct {
	DefaultMode                 string
	AllowNonTransactionalWrites bool
}

func NewRepository(db *mongo.Database) *Repository {
	return NewRepositoryWithMode(db, ModeShadow)
}

func NewRepositoryWithMode(db *mongo.Database, defaultMode string) *Repository {
	return NewRepositoryWithOptions(db, RepositoryOptions{DefaultMode: defaultMode})
}

func NewRepositoryWithOptions(db *mongo.Database, repositoryOptions RepositoryOptions) *Repository {
	if repositoryOptions.DefaultMode != ModeAuthoritative {
		repositoryOptions.DefaultMode = ModeShadow
	}
	return &Repository{
		db:                          db,
		defaultMode:                 repositoryOptions.DefaultMode,
		allowNonTransactionalWrites: repositoryOptions.AllowNonTransactionalWrites,
		startSession:                db.Client().StartSession,
	}
}

func (r *Repository) Mode(ctx context.Context, officeID primitive.ObjectID) (string, error) {
	state, err := r.GetModeState(ctx, officeID)
	return state.Mode, err
}

func (r *Repository) GetModeState(ctx context.Context, officeID primitive.ObjectID) (ModeState, error) {
	var state ModeState
	err := r.db.Collection(modeCollection).FindOne(ctx, bson.M{"office_id": officeID}).Decode(&state)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ModeState{OfficeID: officeID, Mode: r.defaultMode, History: []ModeChange{}}, nil
	}
	if err != nil {
		return ModeState{}, err
	}
	if state.Mode != ModeAuthoritative {
		state.Mode = ModeShadow
	}
	return state, nil
}

func (r *Repository) SetMode(ctx context.Context, officeID primitive.ObjectID, mode string, actorID primitive.ObjectID, reason, reconciliationFrom, reconciliationTo string) (ModeState, bool, error) {
	current, err := r.GetModeState(ctx, officeID)
	if err != nil {
		return ModeState{}, false, err
	}
	if current.Mode == mode {
		return current, false, nil
	}
	now := time.Now().UTC()
	change := ModeChange{
		PreviousMode: current.Mode, Mode: mode, Reason: reason, ChangedBy: actorID, ChangedAt: now,
		ReconciliationFrom: reconciliationFrom, ReconciliationTo: reconciliationTo,
	}
	result := r.db.Collection(modeCollection).FindOneAndUpdate(ctx,
		bson.M{"office_id": officeID},
		bson.M{
			"$setOnInsert": bson.M{"office_id": officeID, "created_at": now},
			"$set":         bson.M{"mode": mode, "updated_by": actorID, "updated_at": now},
			"$push":        bson.M{"history": change},
		},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	)
	var state ModeState
	if err := result.Decode(&state); err != nil {
		return ModeState{}, false, err
	}
	return state, true, nil
}

func (r *Repository) Reconcile(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) (ReconciliationResult, error) {
	return NewReconciler(r).Run(ctx, officeID, startDate, endDate)
}

func (r *Repository) LoadReconciliationLedger(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) ([]LedgerEntry, error) {
	cursor, err := r.db.Collection(ledgerCollection).Find(ctx, bson.M{
		"office_id":        officeID,
		"status":           bson.M{"$ne": StatusVoid},
		"service_date_key": bson.M{"$gte": startDate, "$lte": monthEndDate(endDate)},
		"component": bson.M{"$in": bson.A{
			ComponentSpecialCommission, ComponentGeneralCommission, ComponentUpgradeCommission,
			ComponentTripCommission, ComponentPetrol, ComponentTargetBonus, ComponentLeaderboardBonus,
		}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	entries := make([]LedgerEntry, 0)
	return entries, cursor.All(ctx, &entries)
}

// ClaimNextRebuild atomically claims the oldest queued rebuild. Keeping the
// transition in Mongo prevents two reporting instances from processing the
// same job concurrently.
func (r *Repository) ClaimNextRebuild(ctx context.Context) (RebuildJob, error) {
	now := time.Now().UTC()
	result := r.db.Collection(rebuildCollection).FindOneAndUpdate(ctx,
		bson.M{"status": "queued"},
		bson.M{"$set": bson.M{"status": "running", "started_at": now, "updated_at": now}},
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "requested_at", Value: 1}}).SetReturnDocument(options.After))
	var job RebuildJob
	if err := result.Decode(&job); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return RebuildJob{}, ErrNoQueuedRebuild
		}
		return RebuildJob{}, err
	}
	return job, nil
}

func (r *Repository) LoadOrderSources(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) ([]OrderSource, error) {
	dateRange := bson.M{"$gte": startDate, "$lte": endDate}
	filter := bson.M{
		"office_id":         officeID,
		"is_deleted":        bson.M{"$ne": true},
		"status":            "completed",
		"booking_info.date": dateRange,
	}
	projection := bson.M{
		"_id":                 1,
		"office_id":           1,
		"beautician_id":       1,
		"status":              1,
		"booking_info.date":   1,
		"commission_snapshot": 1,
	}
	cur, err := r.db.Collection("orders").Find(ctx, filter, options.Find().SetProjection(projection))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []OrderSource
	return rows, cur.All(ctx, &rows)
}

func (r *Repository) LoadOrderSource(ctx context.Context, sourceID primitive.ObjectID) (OrderSource, error) {
	var source OrderSource
	err := r.db.Collection("orders").FindOne(ctx, bson.M{"_id": sourceID}, options.FindOne().SetProjection(bson.M{
		"_id": 1, "office_id": 1, "beautician_id": 1, "status": 1,
		"booking_info.date": 1, "commission_snapshot": 1, "is_deleted": 1,
	})).Decode(&source)
	return source, err
}

func (r *Repository) LoadTripSources(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) ([]TripSource, error) {
	// Keep ledger eligibility identical to the static rider and petrol reports.
	// In particular, snapshots on cancelled or in-progress trips must never
	// become payable merely because a rebuild scans their date range.
	filter := payables.TripBaseMatch(startDate, endDate)
	filter["office_id"] = officeID
	cur, err := r.db.Collection("trips").Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []TripSource
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	if needsOfficeTripRates(rows) {
		cost, mileage, err := r.loadOfficeTripRates(ctx, officeID)
		if err != nil {
			return nil, err
		}
		for index := range rows {
			rows[index].OfficePetrolCostPerLiter = cost
			rows[index].OfficeStandardMileagePerLiter = mileage
		}
	}
	return rows, nil
}

func (r *Repository) LoadTripSource(ctx context.Context, sourceID primitive.ObjectID) (TripSource, error) {
	var source TripSource
	err := r.db.Collection("trips").FindOne(ctx, bson.M{"_id": sourceID}).Decode(&source)
	if err == nil && needsOfficeTripRates([]TripSource{source}) {
		source.OfficePetrolCostPerLiter, source.OfficeStandardMileagePerLiter, err = r.loadOfficeTripRates(ctx, source.OfficeID)
	}
	return source, err
}

func needsOfficeTripRates(trips []TripSource) bool {
	for _, trip := range trips {
		if trip.Snapshot != nil && trip.Snapshot.PetrolPayable == nil &&
			(trip.FareCalculation.PetrolCostPerLiter <= 0 || trip.FareCalculation.StandardMileagePerLiter <= 0) {
			return true
		}
	}
	return false
}

func (r *Repository) loadOfficeTripRates(ctx context.Context, officeID primitive.ObjectID) (float64, float64, error) {
	var office struct {
		PetrolCostPerLiter      float64 `bson:"petrol_cost_per_liter"`
		StandardMileagePerLiter float64 `bson:"standard_mileage_per_liter"`
	}
	err := r.db.Collection("offices").FindOne(ctx, bson.M{"_id": officeID}, options.FindOne().SetProjection(bson.M{
		"petrol_cost_per_liter": 1, "standard_mileage_per_liter": 1,
	})).Decode(&office)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, 0, nil
	}
	return office.PetrolCostPerLiter, office.StandardMileagePerLiter, err
}

func (r *Repository) LoadWorkerTargets(ctx context.Context, officeID primitive.ObjectID) ([]WorkerTarget, error) {
	cur, err := r.db.Collection("beauticians").Find(ctx, bson.M{"office_id": officeID, "is_deleted": bson.M{"$ne": true}}, options.Find().SetProjection(bson.M{"_id": 1, "monthly_target1": 1, "monthly_target2": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []WorkerTarget
	return rows, cur.All(ctx, &rows)
}

func (r *Repository) LoadTarget2Bonus(ctx context.Context, officeID primitive.ObjectID) (float64, error) {
	var office struct {
		MonthlyTarget2Bonus float64 `bson:"monthly_target2_bonus"`
	}
	err := r.db.Collection("offices").FindOne(ctx, bson.M{"_id": officeID}, options.FindOne().SetProjection(bson.M{"monthly_target2_bonus": 1})).Decode(&office)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, nil
	}
	return office.MonthlyTarget2Bonus, err
}

func (r *Repository) LoadBeauticianLeaderboardSources(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) ([]BeauticianLeaderboardSource, error) {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, err
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, err
	}
	cursor, err := r.db.Collection("leaderboards").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"office_id": officeID, "date": bson.M{"$gte": start, "$lte": end.Add(24*time.Hour - time.Nanosecond)}}}},
		{{Key: "$group", Value: bson.M{"_id": "$beautician_id", "revenue": bson.M{"$sum": "$revenue"}, "order_count": bson.M{"$sum": "$order_count"}}}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var rows []BeauticianLeaderboardSource
	return rows, cursor.All(ctx, &rows)
}

func (r *Repository) LoadRiderLeaderboardSources(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) ([]RiderLeaderboardSource, error) {
	match := payables.TripBaseMatch(startDate, endDate)
	match["office_id"] = officeID
	workerExpr := payables.AllowanceWorkerIDExpr()
	distanceExpr := payables.PaidSnapshotOrCanonicalExpr("payable_distance_km", payables.PayableDistanceExpr())
	cursor, err := r.db.Collection("trips").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$addFields", Value: bson.M{"allowance_worker_id": workerExpr, "payable_distance_km": distanceExpr, "allowance_worker_type": payables.AllowanceWorkerTypeExpr()}}},
		{{Key: "$match", Value: bson.M{"allowance_worker_id": bson.M{"$ne": nil}}}},
		{{Key: "$group", Value: bson.M{"_id": "$allowance_worker_id", "worker_type": bson.M{"$first": "$allowance_worker_type"}, "trip_count": bson.M{"$sum": 1}, "total_distance_km": bson.M{"$sum": "$payable_distance_km"}}}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var rows []RiderLeaderboardSource
	return rows, cursor.All(ctx, &rows)
}

func (r *Repository) LoadLeaderboardPrizes(ctx context.Context, officeID primitive.ObjectID) (LeaderboardPrizes, error) {
	var office struct {
		LeaderboardPrizes struct {
			Beautician []float64 `bson:"beutician"`
			Rider      []float64 `bson:"rider"`
		} `bson:"leaderboard_prizes"`
	}
	err := r.db.Collection("offices").FindOne(ctx, bson.M{"_id": officeID}, options.FindOne().SetProjection(bson.M{"leaderboard_prizes": 1})).Decode(&office)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return LeaderboardPrizes{}, nil
	}
	return LeaderboardPrizes{Beautician: office.LeaderboardPrizes.Beautician, Rider: office.LeaderboardPrizes.Rider}, err
}

func (r *Repository) PutSourceEntry(ctx context.Context, entry LedgerEntry) (LedgerEntry, bool, error) {
	now := time.Now().UTC()
	entry.ID = primitive.NewObjectID()
	entry.CreatedAt, entry.UpdatedAt = now, now
	result := r.db.Collection(ledgerCollection).FindOneAndUpdate(ctx,
		bson.M{"office_id": entry.OfficeID, "idempotency_key": entry.IdempotencyKey},
		bson.M{"$setOnInsert": entry}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After))
	var stored LedgerEntry
	if err := result.Decode(&stored); err != nil {
		return LedgerEntry{}, false, err
	}
	created := stored.ID == entry.ID
	if created || stored.Status != StatusOpen || stored.SettledAmountPaise != 0 || sameSourceEntry(stored, entry) {
		return stored, created, nil
	}

	// Source entries are materialized projections, not manual ledger actions.
	// Rebuilds may repair an unpaid projection after restored source data or a
	// trip edit. Once any amount is settled, the row remains immutable and the
	// caller reports a conflict so an explicit correction can be posted.
	entry.ID = stored.ID
	entry.CreatedAt = stored.CreatedAt
	entry.CreatedBy = stored.CreatedBy
	entry.UpdatedAt = now
	update := bson.M{"$set": bson.M{
		"worker_id": entry.WorkerID, "worker_type": entry.WorkerType, "service_date_key": entry.ServiceDateKey,
		"component": entry.Component, "settlement_bucket": entry.SettlementBucket,
		"amount_paise": entry.AmountPaise, "settled_amount_paise": entry.SettledAmountPaise, "status": entry.Status,
		"source_type": entry.SourceType, "source_id": entry.SourceID,
		"calculation_version": entry.CalculationVersion, "configuration_snapshot": entry.ConfigurationSnapshot,
		"reason": entry.Reason, "updated_at": now,
	}}
	write, err := r.db.Collection(ledgerCollection).UpdateOne(ctx, bson.M{
		"_id": stored.ID, "status": StatusOpen, "settled_amount_paise": int64(0),
	}, update)
	if err != nil {
		return LedgerEntry{}, false, err
	}
	if write.MatchedCount == 0 {
		return stored, false, nil
	}
	return entry, false, nil
}

func (r *Repository) FinishRebuild(ctx context.Context, id primitive.ObjectID, status string, stats RebuildStats, message string) error {
	_, err := r.db.Collection(rebuildCollection).UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"status": status, "scanned": stats.Scanned, "inserted": stats.Inserted, "unchanged": stats.Unchanged,
		"conflicts": stats.Conflicts, "missing_snapshots": stats.MissingSnapshots, "error_message": message,
		"finished_at": time.Now().UTC(), "updated_at": time.Now().UTC(),
	}})
	return err
}

func (r *Repository) EnsureIndexes(ctx context.Context) error {
	_, err := r.db.Collection(ledgerCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "office_id", Value: 1}, {Key: "idempotency_key", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "office_id", Value: 1}, {Key: "worker_id", Value: 1}, {Key: "service_date_key", Value: -1}}},
		{Keys: bson.D{{Key: "office_id", Value: 1}, {Key: "settlement_bucket", Value: 1}, {Key: "status", Value: 1}, {Key: "service_date_key", Value: 1}}},
		{Keys: bson.D{{Key: "source_type", Value: 1}, {Key: "source_id", Value: 1}}},
	})
	if err != nil {
		return err
	}
	_, err = r.db.Collection(periodCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "office_id", Value: 1}, {Key: "kind", Value: 1}, {Key: "start_date", Value: 1}, {Key: "end_date", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}
	_, err = r.db.Collection(rebuildCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "office_id", Value: 1}, {Key: "idempotency_key", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "office_id", Value: 1}, {Key: "requested_at", Value: -1}}},
	})
	if err != nil {
		return err
	}
	_, err = r.db.Collection(settlementCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "office_id", Value: 1}, {Key: "idempotency_key", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "office_id", Value: 1}, {Key: "worker_id", Value: 1}, {Key: "created_at", Value: -1}}},
	})
	if err != nil {
		return err
	}
	_, err = r.db.Collection(modeCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "office_id", Value: 1}}, Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}
	_, err = r.db.Collection(orderIssueCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "issue_key", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "office_id", Value: 1}, {Key: "status", Value: 1}, {Key: "service_date", Value: -1}}},
		{Keys: bson.D{{Key: "office_id", Value: 1}, {Key: "issue_type", Value: 1}, {Key: "severity", Value: 1}}},
	})
	return err
}

type LedgerFilter struct {
	OfficeID, WorkerID, Component, Bucket, Status, StartDate, EndDate string
	Page, Limit                                                       int64
}

func (r *Repository) ListEntries(ctx context.Context, input LedgerFilter) ([]LedgerEntry, int64, error) {
	filter := bson.M{"office_id": mustObjectID(input.OfficeID)}
	if input.WorkerID != "" {
		filter["worker_id"] = mustObjectID(input.WorkerID)
	}
	if input.Component != "" {
		filter["component"] = input.Component
	}
	if input.Bucket != "" {
		filter["settlement_bucket"] = input.Bucket
	}
	if input.Status != "" {
		filter["status"] = input.Status
	}
	if input.StartDate != "" || input.EndDate != "" {
		rangeFilter := bson.M{}
		if input.StartDate != "" {
			rangeFilter["$gte"] = input.StartDate
		}
		if input.EndDate != "" {
			rangeFilter["$lte"] = input.EndDate
		}
		filter["service_date_key"] = rangeFilter
	}
	total, err := r.db.Collection(ledgerCollection).CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := r.db.Collection(ledgerCollection).Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "service_date_key", Value: -1}, {Key: "created_at", Value: -1}}).
		SetSkip((input.Page-1)*input.Limit).SetLimit(input.Limit))
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	var entries []LedgerEntry
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

type SummaryRow struct {
	Component          Component `bson:"_id" json:"component"`
	AmountPaise        int64     `bson:"amount_paise" json:"amount_paise"`
	SettledAmountPaise int64     `bson:"settled_amount_paise" json:"settled_amount_paise"`
	Count              int64     `bson:"count" json:"count"`
}

func (r *Repository) Summary(ctx context.Context, officeID, startDate, endDate string) ([]SummaryRow, error) {
	match := bson.M{"office_id": mustObjectID(officeID), "status": bson.M{"$ne": StatusVoid}}
	if startDate != "" || endDate != "" {
		dates := bson.M{}
		if startDate != "" {
			dates["$gte"] = startDate
		}
		if endDate != "" {
			dates["$lte"] = endDate
		}
		match["service_date_key"] = dates
	}
	cursor, err := r.db.Collection(ledgerCollection).Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{"_id": "$component", "amount_paise": bson.M{"$sum": "$amount_paise"}, "settled_amount_paise": bson.M{"$sum": "$settled_amount_paise"}, "count": bson.M{"$sum": 1}}}},
		{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var rows []SummaryRow
	return rows, cursor.All(ctx, &rows)
}

func (r *Repository) CreateAdjustment(ctx context.Context, entry LedgerEntry) (LedgerEntry, bool, error) {
	now := time.Now().UTC()
	entry.ID = primitive.NewObjectID()
	entry.CreatedAt, entry.UpdatedAt = now, now
	result := r.db.Collection(ledgerCollection).FindOneAndUpdate(ctx,
		bson.M{"office_id": entry.OfficeID, "idempotency_key": entry.IdempotencyKey},
		bson.M{"$setOnInsert": entry},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	)
	var stored LedgerEntry
	if err := result.Decode(&stored); err != nil {
		return LedgerEntry{}, false, err
	}
	return stored, stored.ID == entry.ID, nil
}

func (r *Repository) OfficeExists(ctx context.Context, officeID primitive.ObjectID) (bool, error) {
	count, err := r.db.Collection("offices").CountDocuments(ctx, bson.M{"_id": officeID})
	return count == 1, err
}

func (r *Repository) ActiveStaffExists(ctx context.Context, staffID primitive.ObjectID) (bool, error) {
	count, err := r.db.Collection("staffs").CountDocuments(ctx, bson.M{
		"_id":        staffID,
		"is_active":  true,
		"is_deleted": bson.M{"$ne": true},
	})
	return count == 1, err
}

func (r *Repository) WorkerBelongsToOffice(ctx context.Context, workerType string, workerID, officeID primitive.ObjectID) (bool, error) {
	collection := "beauticians"
	if workerType == "rider" {
		collection = "riders"
	}
	count, err := r.db.Collection(collection).CountDocuments(ctx, bson.M{
		"_id":        workerID,
		"office_id":  officeID,
		"is_deleted": bson.M{"$ne": true},
	})
	return count == 1, err
}

func (r *Repository) IsDateClosed(ctx context.Context, officeID primitive.ObjectID, serviceDate string) (bool, error) {
	count, err := r.db.Collection(periodCollection).CountDocuments(ctx, bson.M{
		"office_id": officeID,
		"status":    "closed",
		"start_date": bson.M{
			"$lte": serviceDate,
		},
		"end_date": bson.M{
			"$gte": serviceDate,
		},
	})
	return count > 0, err
}

func (r *Repository) HasClosedPeriodOverlap(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) (bool, error) {
	count, err := r.db.Collection(periodCollection).CountDocuments(ctx, bson.M{
		"office_id": officeID,
		"status":    "closed",
		"start_date": bson.M{
			"$lte": endDate,
		},
		"end_date": bson.M{
			"$gte": startDate,
		},
	})
	return count > 0, err
}

// AllocateSettlement creates a settlement and applies its exact allocations.
// Replica-set deployments perform this atomically. Standalone development
// databases may explicitly opt out of transactions. Negative outstanding
// entries (deductions) are consumed first, so a partial payout can never leave
// a misleading zero/negative payable while positive entries were marked paid.
func (r *Repository) AllocateSettlement(ctx context.Context, settlement Settlement) (Settlement, bool, error) {
	var stored Settlement
	created := false
	err := r.runAtomicWrite(ctx, func(writeCtx context.Context) error {
		err := r.db.Collection(settlementCollection).FindOne(writeCtx, bson.M{
			"office_id": settlement.OfficeID, "idempotency_key": settlement.IdempotencyKey,
		}).Decode(&stored)
		if err == nil {
			return nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		filter := bson.M{
			"office_id": settlement.OfficeID, "worker_id": settlement.WorkerID,
			"worker_type": settlement.WorkerType, "settlement_bucket": settlement.Bucket,
			"service_date_key": bson.M{"$gte": settlement.StartDate, "$lte": settlement.EndDate},
			"status":           bson.M{"$in": bson.A{StatusOpen, StatusPartiallySettled}},
		}
		if len(settlement.RequestedEntryIDs) > 0 {
			filter["_id"] = bson.M{"$in": settlement.RequestedEntryIDs}
		}
		cursor, err := r.db.Collection(ledgerCollection).Find(writeCtx, filter, options.Find().SetSort(
			bson.D{{Key: "amount_paise", Value: 1}, {Key: "service_date_key", Value: 1}, {Key: "created_at", Value: 1}},
		))
		if err != nil {
			return err
		}
		var entries []LedgerEntry
		if err := cursor.All(writeCtx, &entries); err != nil {
			_ = cursor.Close(writeCtx)
			return err
		}
		_ = cursor.Close(writeCtx)
		if len(settlement.RequestedEntryIDs) > 0 && len(entries) != len(settlement.RequestedEntryIDs) {
			return ErrSettlementEntriesUnavailable
		}
		allocations, err := buildAllocations(entries, settlement.AmountPaise)
		if err != nil {
			return err
		}
		for _, allocation := range allocations {
			var entry LedgerEntry
			for i := range entries {
				if entries[i].ID == allocation.EntryID {
					entry = entries[i]
					break
				}
			}
			settled := entry.SettledAmountPaise + allocation.AmountPaise
			status := StatusPartiallySettled
			if settled == entry.AmountPaise {
				status = StatusSettled
			}
			result, err := r.db.Collection(ledgerCollection).UpdateOne(writeCtx, bson.M{
				"_id": entry.ID, "office_id": settlement.OfficeID,
				"settled_amount_paise": entry.SettledAmountPaise,
				"status":               bson.M{"$in": bson.A{StatusOpen, StatusPartiallySettled}},
			}, bson.M{"$set": bson.M{"settled_amount_paise": settled, "status": status, "updated_at": time.Now().UTC()}})
			if err != nil {
				return err
			}
			if result.ModifiedCount != 1 {
				return errors.New("ledger changed during settlement allocation; retry with the same idempotency key")
			}
		}
		settlement.ID = primitive.NewObjectID()
		settlement.CreatedAt = time.Now().UTC()
		settlement.UpdatedAt = settlement.CreatedAt
		settlement.Allocations = allocations
		if _, err := r.db.Collection(settlementCollection).InsertOne(writeCtx, settlement); err != nil {
			return err
		}
		stored, created = settlement, true
		return nil
	})
	return stored, created, err
}

func (r *Repository) runAtomicWrite(ctx context.Context, operation func(context.Context) error) error {
	if r.allowNonTransactionalWrites {
		return operation(ctx)
	}
	session, err := r.startSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		return nil, operation(tx)
	})
	return err
}

func (r *Repository) FindSettlement(ctx context.Context, officeID primitive.ObjectID, idempotencyKey string) (Settlement, bool, error) {
	var settlement Settlement
	err := r.db.Collection(settlementCollection).FindOne(ctx, bson.M{
		"office_id": officeID, "idempotency_key": idempotencyKey,
	}).Decode(&settlement)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Settlement{}, false, nil
	}
	return settlement, err == nil, err
}

func (r *Repository) FindSettlementByID(ctx context.Context, officeID, settlementID primitive.ObjectID) (Settlement, bool, error) {
	var settlement Settlement
	err := r.db.Collection(settlementCollection).FindOne(ctx, bson.M{
		"_id": settlementID, "office_id": officeID,
	}).Decode(&settlement)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Settlement{}, false, nil
	}
	return settlement, err == nil, err
}

func settlementEntryStatus(amount, settled int64) EntryStatus {
	if settled == amount {
		return StatusSettled
	}
	if settled == 0 {
		return StatusOpen
	}
	return StatusPartiallySettled
}

// UpdateSettlement reverses the original allocations and applies the edited
// amount again. Replica-set deployments do this in one transaction, keeping the
// ledger totals and entry statuses consistent when an admin corrects a payout.
func (r *Repository) UpdateSettlement(ctx context.Context, officeID, settlementID primitive.ObjectID, update SettlementUpdate) (Settlement, error) {
	var stored Settlement
	err := r.runAtomicWrite(ctx, func(writeCtx context.Context) error {
		if err := r.db.Collection(settlementCollection).FindOne(writeCtx, bson.M{
			"_id": settlementID, "office_id": officeID,
		}).Decode(&stored); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return ErrSettlementNotFound
			}
			return err
		}

		now := time.Now().UTC()
		revision := SettlementRevision{
			AmountPaise: stored.AmountPaise, PaymentMethod: stored.PaymentMethod,
			Reference: stored.Reference, Remarks: stored.Remarks,
			Allocations: stored.Allocations, EditedBy: update.UpdatedBy, EditedAt: now,
		}
		if update.AmountPaise != stored.AmountPaise {
			allocationIDs := make(bson.A, 0, len(stored.Allocations))
			for _, allocation := range stored.Allocations {
				allocationIDs = append(allocationIDs, allocation.EntryID)
			}
			allocatedEntries := make(map[primitive.ObjectID]LedgerEntry, len(stored.Allocations))
			if len(allocationIDs) > 0 {
				cursor, findErr := r.db.Collection(ledgerCollection).Find(writeCtx, bson.M{
					"_id": bson.M{"$in": allocationIDs}, "office_id": officeID,
				})
				if findErr != nil {
					return findErr
				}
				var entries []LedgerEntry
				if findErr = cursor.All(writeCtx, &entries); findErr != nil {
					_ = cursor.Close(writeCtx)
					return findErr
				}
				_ = cursor.Close(writeCtx)
				for _, entry := range entries {
					allocatedEntries[entry.ID] = entry
				}
			}

			for _, allocation := range stored.Allocations {
				entry, found := allocatedEntries[allocation.EntryID]
				if !found {
					return errors.New("settlement allocation references a missing ledger entry")
				}
				settled := entry.SettledAmountPaise - allocation.AmountPaise
				result, updateErr := r.db.Collection(ledgerCollection).UpdateOne(writeCtx, bson.M{
					"_id": entry.ID, "office_id": officeID,
					"settled_amount_paise": entry.SettledAmountPaise,
					"status":               bson.M{"$ne": StatusVoid},
				}, bson.M{"$set": bson.M{
					"settled_amount_paise": settled,
					"status":               settlementEntryStatus(entry.AmountPaise, settled),
					"updated_at":           now,
				}})
				if updateErr != nil {
					return updateErr
				}
				if result.ModifiedCount != 1 {
					return errors.New("ledger changed while reversing the payout; retry the edit")
				}
			}

			filter := bson.M{
				"office_id": officeID, "worker_id": stored.WorkerID,
				"worker_type": stored.WorkerType, "settlement_bucket": stored.Bucket,
				"service_date_key": bson.M{"$gte": stored.StartDate, "$lte": stored.EndDate},
				"status":           bson.M{"$in": bson.A{StatusOpen, StatusPartiallySettled}},
			}
			cursor, findErr := r.db.Collection(ledgerCollection).Find(writeCtx, filter, options.Find().SetSort(
				bson.D{{Key: "amount_paise", Value: 1}, {Key: "service_date_key", Value: 1}, {Key: "created_at", Value: 1}},
			))
			if findErr != nil {
				return findErr
			}
			var entries []LedgerEntry
			if findErr = cursor.All(writeCtx, &entries); findErr != nil {
				_ = cursor.Close(writeCtx)
				return findErr
			}
			_ = cursor.Close(writeCtx)
			allocations, allocationErr := buildAllocations(entries, update.AmountPaise)
			if allocationErr != nil {
				return allocationErr
			}
			entryByID := make(map[primitive.ObjectID]LedgerEntry, len(entries))
			for _, entry := range entries {
				entryByID[entry.ID] = entry
			}
			for _, allocation := range allocations {
				entry := entryByID[allocation.EntryID]
				settled := entry.SettledAmountPaise + allocation.AmountPaise
				result, updateErr := r.db.Collection(ledgerCollection).UpdateOne(writeCtx, bson.M{
					"_id": entry.ID, "office_id": officeID,
					"settled_amount_paise": entry.SettledAmountPaise,
					"status":               bson.M{"$in": bson.A{StatusOpen, StatusPartiallySettled}},
				}, bson.M{"$set": bson.M{
					"settled_amount_paise": settled,
					"status":               settlementEntryStatus(entry.AmountPaise, settled),
					"updated_at":           now,
				}})
				if updateErr != nil {
					return updateErr
				}
				if result.ModifiedCount != 1 {
					return errors.New("ledger changed during payout reallocation; retry the edit")
				}
			}
			stored.Allocations = allocations
		}

		stored.AmountPaise, stored.PaymentMethod = update.AmountPaise, update.PaymentMethod
		stored.Reference, stored.Remarks = update.Reference, update.Remarks
		stored.UpdatedBy, stored.UpdatedAt = update.UpdatedBy, now
		stored.RevisionHistory = append(stored.RevisionHistory, revision)
		result, updateErr := r.db.Collection(settlementCollection).UpdateOne(writeCtx, bson.M{
			"_id": settlementID, "office_id": officeID,
		}, bson.M{
			"$set": bson.M{
				"amount_paise": stored.AmountPaise, "payment_method": stored.PaymentMethod,
				"reference": stored.Reference, "remarks": stored.Remarks,
				"allocations": stored.Allocations, "updated_by": stored.UpdatedBy, "updated_at": stored.UpdatedAt,
			},
			"$push": bson.M{"revision_history": revision},
		})
		if updateErr != nil {
			return updateErr
		}
		if result.MatchedCount != 1 {
			return ErrSettlementNotFound
		}
		return nil
	})
	return stored, err
}

func (r *Repository) ListSettlements(ctx context.Context, input SettlementFilter) ([]Settlement, int64, error) {
	filter := bson.M{"office_id": mustObjectID(input.OfficeID)}
	if input.WorkerID != "" {
		filter["worker_id"] = mustObjectID(input.WorkerID)
	}
	if input.Bucket != "" {
		filter["bucket"] = input.Bucket
	}
	if input.StartDate != "" {
		filter["start_date"] = bson.M{"$gte": input.StartDate}
		filter["end_date"] = bson.M{"$lte": input.EndDate}
	}
	total, err := r.db.Collection(settlementCollection).CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := r.db.Collection(settlementCollection).Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).SetSkip((input.Page-1)*input.Limit).SetLimit(input.Limit))
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	settlements := make([]Settlement, 0)
	if err := cursor.All(ctx, &settlements); err != nil {
		return nil, 0, err
	}
	return settlements, total, nil
}

func buildAllocations(entries []LedgerEntry, requested int64) ([]SettlementAllocation, error) {
	if len(entries) == 0 {
		return nil, ErrNoPendingEarnings
	}
	remaining := requested
	allocations := make([]SettlementAllocation, 0, len(entries))
	// Entries are sorted negative-first by the repository. Fully consuming
	// deductions increases the positive amount that must subsequently be paid.
	for _, entry := range entries {
		outstanding := entry.AmountPaise - entry.SettledAmountPaise
		if outstanding >= 0 {
			continue
		}
		allocations = append(allocations, SettlementAllocation{EntryID: entry.ID, AmountPaise: outstanding})
		remaining -= outstanding
	}
	for _, entry := range entries {
		outstanding := entry.AmountPaise - entry.SettledAmountPaise
		if outstanding <= 0 || remaining == 0 {
			continue
		}
		amount := outstanding
		if amount > remaining {
			amount = remaining
		}
		allocations = append(allocations, SettlementAllocation{EntryID: entry.ID, AmountPaise: amount})
		remaining -= amount
	}
	if remaining != 0 {
		return nil, ErrSettlementExceedsPending
	}
	return allocations, nil
}

func (r *Repository) HasActiveRebuildOverlap(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) (bool, error) {
	count, err := r.db.Collection(rebuildCollection).CountDocuments(ctx, bson.M{
		"office_id": officeID,
		"status":    bson.M{"$in": bson.A{"queued", "running"}},
		"start_date": bson.M{
			"$lte": endDate,
		},
		"end_date": bson.M{
			"$gte": startDate,
		},
	})
	return count > 0, err
}

func (r *Repository) ClosePeriod(ctx context.Context, period Period) (Period, bool, error) {
	now := time.Now().UTC()
	period.ID = primitive.NewObjectID()
	period.Status, period.ClosedAt, period.CreatedAt, period.UpdatedAt = "closed", now, now, now
	result := r.db.Collection(periodCollection).FindOneAndUpdate(ctx,
		bson.M{"office_id": period.OfficeID, "kind": period.Kind, "start_date": period.StartDate, "end_date": period.EndDate},
		bson.M{"$setOnInsert": period}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After))
	var stored Period
	if err := result.Decode(&stored); err != nil {
		return Period{}, false, err
	}
	return stored, stored.ID == period.ID, nil
}

func (r *Repository) QueueRebuild(ctx context.Context, job RebuildJob) (RebuildJob, bool, error) {
	now := time.Now().UTC()
	job.ID = primitive.NewObjectID()
	job.Status, job.RequestedAt, job.UpdatedAt = "queued", now, now
	result := r.db.Collection(rebuildCollection).FindOneAndUpdate(ctx,
		bson.M{"office_id": job.OfficeID, "idempotency_key": job.IdempotencyKey}, bson.M{"$setOnInsert": job},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After))
	var stored RebuildJob
	if err := result.Decode(&stored); err != nil {
		return RebuildJob{}, false, err
	}
	return stored, stored.ID == job.ID, nil
}

func (r *Repository) ListRebuilds(ctx context.Context, input RebuildFilter) ([]RebuildJob, int64, error) {
	filter := bson.M{"office_id": mustObjectID(input.OfficeID)}
	if input.Status != "" {
		filter["status"] = input.Status
	}
	total, err := r.db.Collection(rebuildCollection).CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cur, err := r.db.Collection(rebuildCollection).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "requested_at", Value: -1}}).SetSkip((input.Page-1)*input.Limit).SetLimit(input.Limit))
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)
	var jobs []RebuildJob
	if err := cur.All(ctx, &jobs); err != nil {
		return nil, 0, err
	}
	return jobs, total, nil
}

func (r *Repository) Status(ctx context.Context, officeID string) (map[string]interface{}, error) {
	oid := mustObjectID(officeID)
	ledgerCount, err := r.db.Collection(ledgerCollection).CountDocuments(ctx, bson.M{"office_id": oid})
	if err != nil {
		return nil, err
	}
	openCount, err := r.db.Collection(ledgerCollection).CountDocuments(ctx, bson.M{"office_id": oid, "status": bson.M{"$in": bson.A{StatusOpen, StatusPartiallySettled}}})
	if err != nil {
		return nil, err
	}
	queuedRebuilds, err := r.db.Collection(rebuildCollection).CountDocuments(ctx, bson.M{"office_id": oid, "status": bson.M{"$in": bson.A{"queued", "running"}}})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"ledger_entries": ledgerCount, "open_entries": openCount, "queued_rebuilds": queuedRebuilds}, nil
}

func mustObjectID(value string) primitive.ObjectID {
	id, _ := primitive.ObjectIDFromHex(value)
	return id
}

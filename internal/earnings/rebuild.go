package earnings

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/leaderboard"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

var ErrNoQueuedRebuild = errors.New("no queued earnings rebuild")

const leaderboardCalculationVersion = 2

type CommissionSnapshot struct {
	OrderCost              *float64  `bson:"order_cost"`
	SpecialCommission      *float64  `bson:"special_commission"`
	GeneralCommission      *float64  `bson:"general_commission"`
	UpgradeAddonCommission *float64  `bson:"upgrade_addon_commission"`
	IsPaid                 bool      `bson:"is_paid"`
	CapturedAt             time.Time `bson:"captured_at"`
}

type OrderBookingInfo struct {
	Date string `bson:"date"`
}

type OrderSource struct {
	ID           primitive.ObjectID  `bson:"_id"`
	OfficeID     primitive.ObjectID  `bson:"office_id"`
	BeauticianID primitive.ObjectID  `bson:"beautician_id"`
	Status       string              `bson:"status"`
	IsDeleted    bool                `bson:"is_deleted"`
	BookingInfo  OrderBookingInfo    `bson:"booking_info"`
	Snapshot     *CommissionSnapshot `bson:"commission_snapshot"`
}

type PayableSnapshot struct {
	PayableDistanceKM       *float64  `bson:"payable_distance_km"`
	CommissionPayable       *float64  `bson:"commission_payable"`
	PetrolPayable           *float64  `bson:"petrol_payable"`
	PetrolCostPerLiter      *float64  `bson:"petrol_cost_per_liter"`
	StandardMileagePerLiter *float64  `bson:"standard_mileage_per_liter"`
	CommissionRatePerKM     *float64  `bson:"rider_commission_rate_per_km"`
	IsPaid                  bool      `bson:"is_paid"`
	CapturedAt              time.Time `bson:"captured_at"`
}

type TripFareCalculation struct {
	TripDistanceKM          float64 `bson:"trip_distance_km"`
	CalculatedFare          float64 `bson:"calculated_fare"`
	PetrolCostPerLiter      float64 `bson:"petrol_cost_per_liter"`
	StandardMileagePerLiter float64 `bson:"standard_mileage_per_liter"`
}

type TripSource struct {
	ID                 primitive.ObjectID  `bson:"_id"`
	OfficeID           primitive.ObjectID  `bson:"office_id"`
	RiderID            *primitive.ObjectID `bson:"rider_id"`
	DriverBeauticianID *primitive.ObjectID `bson:"driver_beautician_id"`
	BeauticianID       *primitive.ObjectID `bson:"beautician_id"`
	IsSelfDrive        bool                `bson:"is_self_drive"`
	Date               string              `bson:"date"`
	Status             string              `bson:"status"`
	KanbanState        string              `bson:"kanban_state"`
	IsDeleted          bool                `bson:"is_deleted"`
	IsTwoWay           bool                `bson:"is_two_way"`
	IsCommissionable   bool                `bson:"is_commission_applicable"`
	CommissionAmount   float64             `bson:"commission_amount"`
	IsManualDistance   bool                `bson:"is_distance_manually_overridden"`
	AutoDistanceKM     float64             `bson:"auto_distance_km"`
	ExtraKM            float64             `bson:"extra_km"`
	FareCalculation    TripFareCalculation `bson:"fare_calculation"`
	Snapshot           *PayableSnapshot    `bson:"payable_snapshot"`
}

type WorkerTarget struct {
	WorkerID primitive.ObjectID `bson:"_id"`
	Target1  float64            `bson:"monthly_target1"`
	Target2  float64            `bson:"monthly_target2"`
}

type BeauticianLeaderboardSource struct {
	WorkerID   primitive.ObjectID `bson:"_id"`
	Revenue    float64            `bson:"revenue"`
	OrderCount int                `bson:"order_count"`
}

type RiderLeaderboardSource struct {
	WorkerID        primitive.ObjectID `bson:"_id"`
	WorkerType      string             `bson:"worker_type"`
	TripCount       int                `bson:"trip_count"`
	TotalDistanceKM float64            `bson:"total_distance_km"`
}

type LeaderboardPrizes struct {
	Beautician []float64
	Rider      []float64
}

// leaderboardConfiguration is a content-addressed snapshot of the effective
// office prize schedules used by a rebuild. Converting to paise makes the
// fingerprint match the precision that can actually enter the ledger.
type leaderboardConfiguration struct {
	Version               string
	BeauticianPrizesPaise []int64
	RiderPrizesPaise      []int64
}

type RebuildBackend interface {
	ClaimNextRebuild(context.Context) (RebuildJob, error)
	LoadOrderSources(context.Context, primitive.ObjectID, string, string) ([]OrderSource, error)
	LoadTripSources(context.Context, primitive.ObjectID, string, string) ([]TripSource, error)
	LoadWorkerTargets(context.Context, primitive.ObjectID) ([]WorkerTarget, error)
	LoadTarget2Bonus(context.Context, primitive.ObjectID) (float64, error)
	LoadBeauticianLeaderboardSources(context.Context, primitive.ObjectID, string, string) ([]BeauticianLeaderboardSource, error)
	LoadRiderLeaderboardSources(context.Context, primitive.ObjectID, string, string) ([]RiderLeaderboardSource, error)
	LoadLeaderboardPrizes(context.Context, primitive.ObjectID) (LeaderboardPrizes, error)
	PutSourceEntry(context.Context, LedgerEntry) (LedgerEntry, bool, error)
	FinishRebuild(context.Context, primitive.ObjectID, string, RebuildStats, string) error
}

type RebuildStats struct {
	Scanned          int64
	Inserted         int64
	Unchanged        int64
	Conflicts        int64
	MissingSnapshots int64
}

type Processor struct{ backend RebuildBackend }

func NewProcessor(backend RebuildBackend) *Processor { return &Processor{backend: backend} }

func (p *Processor) ProcessNext(ctx context.Context) (bool, error) {
	job, err := p.backend.ClaimNextRebuild(ctx)
	if errors.Is(err, ErrNoQueuedRebuild) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	stats, processErr := p.process(ctx, job)
	status, message := "completed", ""
	if stats.Conflicts > 0 || stats.MissingSnapshots > 0 {
		status = "completed_with_issues"
	}
	if processErr != nil {
		status, message = "failed", processErr.Error()
	}
	if err := p.backend.FinishRebuild(ctx, job.ID, status, stats, message); err != nil {
		return true, err
	}
	return true, processErr
}

func (p *Processor) process(ctx context.Context, job RebuildJob) (RebuildStats, error) {
	stats := RebuildStats{}
	if job.Scope == "all" || job.Scope == "commissions" {
		if err := p.processOrders(ctx, job, &stats); err != nil {
			return stats, err
		}
	}
	if job.Scope == "all" || job.Scope == "commissions" || job.Scope == "petrol" {
		if err := p.processTrips(ctx, job, &stats); err != nil {
			return stats, err
		}
	}
	if job.Scope == "leaderboards" || job.Scope == "all" {
		if err := p.processLeaderboards(ctx, job, &stats); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func (p *Processor) processLeaderboards(ctx context.Context, job RebuildJob, stats *RebuildStats) error {
	beauticians, err := p.backend.LoadBeauticianLeaderboardSources(ctx, job.OfficeID, job.StartDate, job.EndDate)
	if err != nil {
		return err
	}
	riders, err := p.backend.LoadRiderLeaderboardSources(ctx, job.OfficeID, job.StartDate, job.EndDate)
	if err != nil {
		return err
	}
	prizes, err := p.backend.LoadLeaderboardPrizes(ctx, job.OfficeID)
	if err != nil {
		return err
	}
	if !validPrizes(prizes.Beautician) || !validPrizes(prizes.Rider) {
		return errors.New("leaderboard prizes must be finite non-negative amounts")
	}
	configuration := snapshotLeaderboardConfiguration(prizes)

	beauticianScores := make([]leaderboard.BeauticianScore, 0, len(beauticians))
	seenBeauticians := make(map[primitive.ObjectID]struct{}, len(beauticians))
	for _, row := range beauticians {
		stats.Scanned++
		if row.WorkerID.IsZero() || !validMoney(row.Revenue) || row.OrderCount < 0 {
			stats.MissingSnapshots++
			return errors.New("invalid beautician leaderboard aggregate")
		}
		if _, exists := seenBeauticians[row.WorkerID]; exists {
			return errors.New("duplicate beautician leaderboard aggregate")
		}
		seenBeauticians[row.WorkerID] = struct{}{}
		beauticianScores = append(beauticianScores, leaderboard.BeauticianScore{WorkerID: row.WorkerID, Revenue: row.Revenue, OrderCount: row.OrderCount})
	}

	riderScores := make([]leaderboard.RiderScore, 0, len(riders))
	typeByWorker := make(map[primitive.ObjectID]string, len(riders))
	for _, row := range riders {
		stats.Scanned++
		if row.WorkerID.IsZero() || (row.WorkerType != "rider" && row.WorkerType != "beautician") || row.TripCount < 0 || !validMoney(row.TotalDistanceKM) {
			stats.MissingSnapshots++
			return errors.New("invalid rider leaderboard aggregate")
		}
		if _, exists := typeByWorker[row.WorkerID]; exists {
			return errors.New("duplicate rider leaderboard aggregate")
		}
		typeByWorker[row.WorkerID] = row.WorkerType
		riderScores = append(riderScores, leaderboard.RiderScore{WorkerID: row.WorkerID, TripCount: row.TripCount, TotalDistanceKM: row.TotalDistanceKM})
	}
	// Validate and rank the complete source set before writing any awards. This
	// prevents a malformed rider row from leaving beautician awards partially
	// materialized in the same rebuild.
	beauticianAwards := leaderboard.RankBeauticians(beauticianScores, prizes.Beautician)
	riderAwards := leaderboard.RankRiders(riderScores, prizes.Rider)
	for _, award := range beauticianAwards {
		if err := p.putLeaderboardAward(ctx, job, award, "beautician", "beautician", configuration, stats); err != nil {
			return err
		}
	}
	for _, award := range riderAwards {
		if err := p.putLeaderboardAward(ctx, job, award, typeByWorker[award.WorkerID], "rider", configuration, stats); err != nil {
			return err
		}
	}
	return nil
}

func (p *Processor) putLeaderboardAward(ctx context.Context, job RebuildJob, award leaderboard.Award, workerType, leaderboardType string, configuration leaderboardConfiguration, stats *RebuildStats) error {
	if award.Bonus == 0 {
		return nil
	}
	prizeSchedule := configuration.RiderPrizesPaise
	rankingContract := "trip_count_desc,total_distance_km_desc,worker_id_asc"
	if leaderboardType == "beautician" {
		prizeSchedule = configuration.BeauticianPrizesPaise
		rankingContract = "revenue_desc,order_count_desc,worker_id_asc"
	}
	entry := LedgerEntry{
		OfficeID: job.OfficeID, WorkerID: award.WorkerID, WorkerType: workerType, ServiceDateKey: job.EndDate,
		Component: ComponentLeaderboardBonus, SettlementBucket: BucketCommission, AmountPaise: moneyToPaise(award.Bonus), Status: StatusOpen,
		SourceType: "leaderboards", CalculationVersion: leaderboardCalculationVersion, CreatedBy: job.RequestedBy,
		// The board category, rather than the worker's employment type, identifies
		// the award. A beautician may also appear on the rider board through
		// self-drive work and those are two distinct payables. Keep that logical
		// identity stable when the calculation/configuration version changes so a
		// replay conflicts rather than inserting another copy.
		IdempotencyKey: fmt.Sprintf("leaderboard:%s:%s:%s:%s:%s:v1", job.OfficeID.Hex(), leaderboardType, job.StartDate, job.EndDate, award.WorkerID.Hex()),
		ConfigurationSnapshot: map[string]interface{}{
			"period_start": job.StartDate, "period_end": job.EndDate,
			"leaderboard_type": leaderboardType, "rank": award.Rank,
			"prize_paise": moneyToPaise(award.Bonus), "prize_schedule_paise": append([]int64(nil), prizeSchedule...),
			"configuration_version": configuration.Version, "ranking_contract": rankingContract,
		},
	}
	return p.put(ctx, entry, stats)
}

func snapshotLeaderboardConfiguration(prizes LeaderboardPrizes) leaderboardConfiguration {
	configuration := leaderboardConfiguration{
		BeauticianPrizesPaise: prizesToPaise(prizes.Beautician),
		RiderPrizesPaise:      prizesToPaise(prizes.Rider),
	}
	canonical := fmt.Sprintf("beautician=%v;rider=%v", configuration.BeauticianPrizesPaise, configuration.RiderPrizesPaise)
	configuration.Version = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(canonical)))
	return configuration
}

func prizesToPaise(prizes []float64) []int64 {
	result := make([]int64, len(prizes))
	for index, prize := range prizes {
		result[index] = moneyToPaise(prize)
	}
	return result
}

func validPrizes(prizes []float64) bool {
	for _, prize := range prizes {
		if !validMoney(prize) {
			return false
		}
	}
	return true
}

func (p *Processor) processOrders(ctx context.Context, job RebuildJob, stats *RebuildStats) error {
	monthStart, monthEnd, err := coveringMonths(job.StartDate, job.EndDate)
	if err != nil {
		return err
	}
	orders, err := p.backend.LoadOrderSources(ctx, job.OfficeID, monthStart, monthEnd)
	if err != nil {
		return err
	}
	targets, err := p.backend.LoadWorkerTargets(ctx, job.OfficeID)
	if err != nil {
		return err
	}
	target2Bonus, err := p.backend.LoadTarget2Bonus(ctx, job.OfficeID)
	if err != nil {
		return err
	}
	if !validMoney(target2Bonus) {
		return errors.New("monthly target 2 bonus must be a finite non-negative amount")
	}
	targetByWorker := make(map[primitive.ObjectID]float64, len(targets))
	target2ByWorker := make(map[primitive.ObjectID]float64, len(targets))
	for _, target := range targets {
		if target.WorkerID.IsZero() || !validMoney(target.Target1) || !validMoney(target.Target2) {
			return errors.New("worker targets must be finite non-negative amounts")
		}
		targetByWorker[target.WorkerID] = target.Target1
		target2ByWorker[target.WorkerID] = target.Target2
	}
	revenue := map[string]float64{}
	invalidMonth := map[string]bool{}
	for _, order := range orders {
		if order.Status != "completed" {
			continue
		}
		key := workerMonthKey(order.BeauticianID, orderDate(order))
		if order.Snapshot == nil || order.Snapshot.OrderCost == nil || !validMoney(*order.Snapshot.OrderCost) {
			invalidMonth[key] = true
			continue
		}
		revenue[key] += *order.Snapshot.OrderCost
	}
	for _, order := range orders {
		// The repository excludes non-completed orders. Keep the same guard at
		// materialization time so an alternate backend or future query change
		// cannot turn cancelled/refunded snapshots into earnings.
		if order.Status != "completed" {
			continue
		}
		serviceDate := orderDate(order)
		if !validSourceDate(serviceDate) {
			stats.MissingSnapshots++
			continue
		}
		if serviceDate < job.StartDate || serviceDate > job.EndDate {
			continue
		}
		stats.Scanned++
		if order.Snapshot == nil {
			stats.MissingSnapshots++
			continue
		}
		components := []struct {
			component Component
			amount    *float64
			enabled   bool
		}{
			{ComponentSpecialCommission, order.Snapshot.SpecialCommission, true},
			{ComponentGeneralCommission, order.Snapshot.GeneralCommission, !invalidMonth[workerMonthKey(order.BeauticianID, serviceDate)] && revenue[workerMonthKey(order.BeauticianID, serviceDate)] >= targetByWorker[order.BeauticianID]},
			{ComponentUpgradeCommission, order.Snapshot.UpgradeAddonCommission, true},
		}
		for _, item := range components {
			if item.amount == nil || !validMoney(*item.amount) {
				stats.MissingSnapshots++
				continue
			}
			if !item.enabled || *item.amount == 0 {
				continue
			}
			entry := sourceEntry(job, order.ID, order.BeauticianID, "beautician", serviceDate, item.component, BucketCommission, moneyToPaise(*item.amount), order.Snapshot.IsPaid)
			if err := p.put(ctx, entry, stats); err != nil {
				return err
			}
		}
	}
	// The static beautician report evaluates Target 2 against the complete
	// calendar month containing end_date and pays the office-configured bonus
	// once per worker. Use a month-scoped key so overlapping rebuild requests
	// cannot create duplicate bonuses, and snapshot every input so later office
	// or worker configuration changes are surfaced as a rebuild conflict.
	targetMonth := job.EndDate[:7]
	targetMonthEnd := monthEndDate(job.EndDate)
	for workerID, target2 := range target2ByWorker {
		key := workerMonthKey(workerID, job.EndDate)
		if target2 <= 0 || target2Bonus == 0 || invalidMonth[key] || revenue[key] < target2 {
			continue
		}
		entry := LedgerEntry{
			OfficeID: job.OfficeID, WorkerID: workerID, WorkerType: "beautician", ServiceDateKey: targetMonthEnd,
			Component: ComponentTargetBonus, SettlementBucket: BucketCommission, AmountPaise: moneyToPaise(target2Bonus), Status: StatusOpen,
			SourceType: "targets", CalculationVersion: 1, CreatedBy: job.RequestedBy,
			IdempotencyKey: fmt.Sprintf("target_bonus:%s:%s:%s:v1", job.OfficeID.Hex(), workerID.Hex(), targetMonth),
			ConfigurationSnapshot: map[string]interface{}{
				"target_month": targetMonth, "monthly_target2": target2, "monthly_revenue": revenue[key], "monthly_target2_bonus": target2Bonus,
			},
		}
		if err := p.put(ctx, entry, stats); err != nil {
			return err
		}
	}
	return nil
}

func (p *Processor) processTrips(ctx context.Context, job RebuildJob, stats *RebuildStats) error {
	trips, err := p.backend.LoadTripSources(ctx, job.OfficeID, job.StartDate, job.EndDate)
	if err != nil {
		return err
	}
	for _, trip := range trips {
		stats.Scanned++
		if !validSourceDate(trip.Date) {
			stats.MissingSnapshots++
			continue
		}
		workerID, workerType, ok := tripWorker(trip)
		if !ok || trip.Snapshot == nil {
			stats.MissingSnapshots++
			continue
		}
		commissionPayable, petrolPayable := effectiveTripPayables(trip)
		items := []struct {
			component Component
			bucket    SettlementBucket
			amount    *float64
			enabled   bool
		}{
			{ComponentTripCommission, BucketCommission, commissionPayable, job.Scope != "petrol"},
			{ComponentPetrol, BucketPetrol, petrolPayable, true},
		}
		for _, item := range items {
			if !item.enabled {
				continue
			}
			if item.amount == nil || !validMoney(*item.amount) {
				stats.MissingSnapshots++
				continue
			}
			if *item.amount == 0 {
				continue
			}
			entry := sourceEntry(job, trip.ID, workerID, workerType, trip.Date, item.component, item.bucket, moneyToPaise(*item.amount), trip.Snapshot.IsPaid)
			if err := p.put(ctx, entry, stats); err != nil {
				return err
			}
		}
	}
	return nil
}

func effectiveTripPayables(trip TripSource) (*float64, *float64) {
	if trip.Snapshot == nil {
		return nil, nil
	}
	// Settled snapshots are immutable. Corrections for paid earnings must use
	// an auditable adjustment instead of rewriting the source entry.
	if trip.Snapshot.IsPaid && trip.Snapshot.CommissionPayable != nil && trip.Snapshot.PetrolPayable != nil {
		return trip.Snapshot.CommissionPayable, trip.Snapshot.PetrolPayable
	}
	distance := trip.FareCalculation.TripDistanceKM
	if !trip.IsManualDistance && trip.AutoDistanceKM > 0 {
		distance = trip.AutoDistanceKM + trip.ExtraKM
		if trip.IsTwoWay {
			distance = trip.AutoDistanceKM*2 + trip.ExtraKM
		}
	}
	if distance <= 0 {
		return trip.Snapshot.CommissionPayable, trip.Snapshot.PetrolPayable
	}
	rate := 1.0
	if trip.Snapshot.CommissionRatePerKM != nil && *trip.Snapshot.CommissionRatePerKM > 0 {
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
	if trip.Snapshot.PetrolCostPerLiter != nil && *trip.Snapshot.PetrolCostPerLiter > 0 {
		cost = *trip.Snapshot.PetrolCostPerLiter
	}
	if trip.Snapshot.StandardMileagePerLiter != nil && *trip.Snapshot.StandardMileagePerLiter > 0 {
		mileage = *trip.Snapshot.StandardMileagePerLiter
	}
	if cost <= 0 || mileage <= 0 {
		return &commission, trip.Snapshot.PetrolPayable
	}
	petrol := roundSourceMoney(distance / mileage * cost)
	if trip.Snapshot.IsPaid {
		if trip.Snapshot.CommissionPayable != nil {
			commission = *trip.Snapshot.CommissionPayable
		}
		if trip.Snapshot.PetrolPayable != nil {
			petrol = *trip.Snapshot.PetrolPayable
		}
	}
	return &commission, &petrol
}

func roundSourceMoney(value float64) float64 { return math.Round(value*100) / 100 }

func (p *Processor) put(ctx context.Context, entry LedgerEntry, stats *RebuildStats) error {
	return putLedgerEntry(ctx, p.backend, entry, stats)
}

type sourceEntryPutter interface {
	PutSourceEntry(context.Context, LedgerEntry) (LedgerEntry, bool, error)
}

func putLedgerEntry(ctx context.Context, backend sourceEntryPutter, entry LedgerEntry, stats *RebuildStats) error {
	stored, created, err := backend.PutSourceEntry(ctx, entry)
	if err != nil {
		return err
	}
	if created {
		stats.Inserted++
		return nil
	}
	if sameSourceEntry(stored, entry) {
		stats.Unchanged++
	} else {
		stats.Conflicts++
	}
	return nil
}

func sourceEntry(job RebuildJob, sourceID, workerID primitive.ObjectID, workerType, date string, component Component, bucket SettlementBucket, amount int64, paid bool) LedgerEntry {
	status, settled := StatusOpen, int64(0)
	if paid {
		status, settled = StatusSettled, amount
	}
	return LedgerEntry{
		OfficeID: job.OfficeID, WorkerID: workerID, WorkerType: workerType, ServiceDateKey: date,
		Component: component, SettlementBucket: bucket, AmountPaise: amount, SettledAmountPaise: settled,
		Status: status, SourceType: sourceCollection(component), SourceID: &sourceID, CalculationVersion: 1,
		IdempotencyKey: fmt.Sprintf("source:%s:%s:%s:v1", sourceCollection(component), sourceID.Hex(), component), CreatedBy: job.RequestedBy,
	}
}

func sourceCollection(component Component) string {
	if component == ComponentLeaderboardBonus {
		return "leaderboards"
	}
	if component == ComponentTripCommission || component == ComponentPetrol {
		return "trips"
	}
	return "orders"
}

func sameSourceEntry(left, right LedgerEntry) bool {
	return left.OfficeID == right.OfficeID && left.WorkerID == right.WorkerID && left.WorkerType == right.WorkerType &&
		left.ServiceDateKey == right.ServiceDateKey && left.Component == right.Component && left.SettlementBucket == right.SettlementBucket &&
		left.AmountPaise == right.AmountPaise && left.SettledAmountPaise == right.SettledAmountPaise && left.Status == right.Status &&
		left.SourceType == right.SourceType && left.CalculationVersion == right.CalculationVersion && sameConfigurationSnapshot(left.ConfigurationSnapshot, right.ConfigurationSnapshot)
}

func sameConfigurationSnapshot(left, right map[string]interface{}) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		rightValue, ok := right[key]
		if !ok || !sameConfigurationValue(leftValue, rightValue) {
			return false
		}
	}
	return true
}

func sameConfigurationValue(left, right interface{}) bool {
	leftNumber, leftIsNumber := numericConfigurationValue(left)
	rightNumber, rightIsNumber := numericConfigurationValue(right)
	if leftIsNumber || rightIsNumber {
		return leftIsNumber && rightIsNumber && leftNumber == rightNumber
	}
	leftItems, leftIsSlice := configurationSlice(left)
	rightItems, rightIsSlice := configurationSlice(right)
	if leftIsSlice || rightIsSlice {
		if !leftIsSlice || !rightIsSlice || len(leftItems) != len(rightItems) {
			return false
		}
		for index := range leftItems {
			if !sameConfigurationValue(leftItems[index], rightItems[index]) {
				return false
			}
		}
		return true
	}
	return fmt.Sprint(left) == fmt.Sprint(right)
}

func configurationSlice(value interface{}) ([]interface{}, bool) {
	if value == nil {
		return nil, false
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Array && reflected.Kind() != reflect.Slice {
		return nil, false
	}
	items := make([]interface{}, reflected.Len())
	for index := range items {
		items[index] = reflected.Index(index).Interface()
	}
	return items, true
}

func numericConfigurationValue(value interface{}) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func tripWorker(trip TripSource) (primitive.ObjectID, string, bool) {
	if trip.DriverBeauticianID != nil {
		return *trip.DriverBeauticianID, "beautician", true
	}
	if trip.IsSelfDrive && trip.BeauticianID != nil {
		return *trip.BeauticianID, "beautician", true
	}
	if trip.RiderID != nil {
		return *trip.RiderID, "rider", true
	}
	return primitive.NilObjectID, "", false
}

func validMoney(value float64) bool    { return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 }
func moneyToPaise(value float64) int64 { return int64(math.Round(value * 100)) }

// workerMonthKey groups source rows by worker and calendar month. Source data
// is external and may contain malformed/short date strings; never panic while
// rebuilding because of one bad row.
func workerMonthKey(worker primitive.ObjectID, date string) string {
	month := date
	if len(date) >= 7 {
		month = date[:7]
	}
	return worker.Hex() + ":" + month
}

func orderDate(order OrderSource) string {
	return order.BookingInfo.Date
}

func validSourceDate(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func coveringMonths(start, end string) (string, string, error) {
	startDate, err := time.Parse("2006-01-02", start)
	if err != nil {
		return "", "", err
	}
	endDate, err := time.Parse("2006-01-02", end)
	if err != nil {
		return "", "", err
	}
	first := time.Date(startDate.Year(), startDate.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(endDate.Year(), endDate.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	return first.Format("2006-01-02"), last.Format("2006-01-02"), nil
}

func monthEndDate(value string) string {
	date, _ := time.Parse("2006-01-02", value) // processOrders validates the range first.
	return time.Date(date.Year(), date.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

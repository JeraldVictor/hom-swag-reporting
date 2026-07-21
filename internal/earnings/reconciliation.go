package earnings

import (
	"context"
	"errors"
	"sort"

	"github.com/JeraldVictor/hom-swag-reporting/internal/leaderboard"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ReconciliationRow struct {
	WorkerID        primitive.ObjectID `json:"worker_id"`
	WorkerType      string             `json:"worker_type"`
	Scope           string             `json:"scope"`
	Component       Component          `json:"component"`
	Bucket          SettlementBucket   `json:"bucket"`
	ExpectedPaise   int64              `json:"expected_paise"`
	LedgerPaise     int64              `json:"ledger_paise"`
	DifferencePaise int64              `json:"difference_paise"`
	Status          string             `json:"status"`
}

type ReconciliationResult struct {
	OfficeID                primitive.ObjectID  `json:"office_id"`
	StartDate               string              `json:"start_date"`
	EndDate                 string              `json:"end_date"`
	Ready                   bool                `json:"ready"`
	ExpectedPaise           int64               `json:"expected_paise"`
	LedgerPaise             int64               `json:"ledger_paise"`
	DifferencePaise         int64               `json:"difference_paise"`
	AbsoluteDifferencePaise int64               `json:"absolute_difference_paise"`
	Matched                 int                 `json:"matched"`
	Mismatched              int                 `json:"mismatched"`
	MissingSnapshots        int64               `json:"missing_snapshots"`
	Rows                    []ReconciliationRow `json:"rows"`
}

type ReconciliationBackend interface {
	LoadOrderSources(context.Context, primitive.ObjectID, string, string) ([]OrderSource, error)
	LoadTripSources(context.Context, primitive.ObjectID, string, string) ([]TripSource, error)
	LoadWorkerTargets(context.Context, primitive.ObjectID) ([]WorkerTarget, error)
	LoadTarget2Bonus(context.Context, primitive.ObjectID) (float64, error)
	LoadBeauticianLeaderboardSources(context.Context, primitive.ObjectID, string, string) ([]BeauticianLeaderboardSource, error)
	LoadRiderLeaderboardSources(context.Context, primitive.ObjectID, string, string) ([]RiderLeaderboardSource, error)
	LoadLeaderboardPrizes(context.Context, primitive.ObjectID) (LeaderboardPrizes, error)
	LoadReconciliationLedger(context.Context, primitive.ObjectID, string, string) ([]LedgerEntry, error)
}

type Reconciler struct{ backend ReconciliationBackend }

func NewReconciler(backend ReconciliationBackend) *Reconciler { return &Reconciler{backend: backend} }

type reconciliationKey struct {
	workerID   primitive.ObjectID
	workerType string
	scope      string
	component  Component
	bucket     SettlementBucket
}

func (r *Reconciler) Run(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) (ReconciliationResult, error) {
	result := ReconciliationResult{OfficeID: officeID, StartDate: startDate, EndDate: endDate, Rows: []ReconciliationRow{}}
	monthStart, monthEnd, err := coveringMonths(startDate, endDate)
	if err != nil {
		return result, err
	}
	orders, err := r.backend.LoadOrderSources(ctx, officeID, monthStart, monthEnd)
	if err != nil {
		return result, err
	}
	trips, err := r.backend.LoadTripSources(ctx, officeID, startDate, endDate)
	if err != nil {
		return result, err
	}
	targets, err := r.backend.LoadWorkerTargets(ctx, officeID)
	if err != nil {
		return result, err
	}
	target2Bonus, err := r.backend.LoadTarget2Bonus(ctx, officeID)
	if err != nil {
		return result, err
	}
	beauticians, err := r.backend.LoadBeauticianLeaderboardSources(ctx, officeID, startDate, endDate)
	if err != nil {
		return result, err
	}
	riders, err := r.backend.LoadRiderLeaderboardSources(ctx, officeID, startDate, endDate)
	if err != nil {
		return result, err
	}
	prizes, err := r.backend.LoadLeaderboardPrizes(ctx, officeID)
	if err != nil {
		return result, err
	}
	ledgerEntries, err := r.backend.LoadReconciliationLedger(ctx, officeID, startDate, endDate)
	if err != nil {
		return result, err
	}
	if !validMoney(target2Bonus) || !validPrizes(prizes.Beautician) || !validPrizes(prizes.Rider) {
		return result, errors.New("reconciliation configuration must contain finite non-negative amounts")
	}

	expected := map[reconciliationKey]int64{}
	target1 := map[primitive.ObjectID]float64{}
	target2 := map[primitive.ObjectID]float64{}
	for _, target := range targets {
		if target.WorkerID.IsZero() || !validMoney(target.Target1) || !validMoney(target.Target2) {
			return result, errors.New("worker targets must be finite non-negative amounts")
		}
		target1[target.WorkerID], target2[target.WorkerID] = target.Target1, target.Target2
	}
	revenue := map[string]float64{}
	invalidMonth := map[string]bool{}
	for _, order := range orders {
		if order.Status != "completed" || order.IsDeleted {
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
		serviceDate := orderDate(order)
		if order.Status != "completed" || order.IsDeleted || serviceDate < startDate || serviceDate > endDate {
			continue
		}
		if order.ID.IsZero() || order.BeauticianID.IsZero() || !validSourceDate(serviceDate) || order.Snapshot == nil {
			result.MissingSnapshots++
			continue
		}
		workerKey := workerMonthKey(order.BeauticianID, serviceDate)
		components := []struct {
			component Component
			amount    *float64
			enabled   bool
		}{
			{ComponentSpecialCommission, order.Snapshot.SpecialCommission, true},
			{ComponentGeneralCommission, order.Snapshot.GeneralCommission, !invalidMonth[workerKey] && revenue[workerKey] >= target1[order.BeauticianID]},
			{ComponentUpgradeCommission, order.Snapshot.UpgradeAddonCommission, true},
		}
		for _, item := range components {
			if item.amount == nil || !validMoney(*item.amount) {
				result.MissingSnapshots++
				continue
			}
			if item.enabled {
				addReconciliationAmount(expected, order.BeauticianID, "beautician", item.component, BucketCommission, moneyToPaise(*item.amount))
			}
		}
	}
	targetMonth := endDate[:7]
	for workerID, threshold := range target2 {
		key := workerMonthKey(workerID, endDate)
		if threshold > 0 && !invalidMonth[key] && revenue[key] >= threshold {
			addReconciliationAmount(expected, workerID, "beautician", ComponentTargetBonus, BucketCommission, moneyToPaise(target2Bonus))
		}
	}

	for _, trip := range trips {
		if !tripEligible(trip) || trip.Date < startDate || trip.Date > endDate {
			continue
		}
		workerID, workerType, ok := tripWorker(trip)
		if !ok {
			continue
		}
		if trip.Snapshot == nil || !validSourceDate(trip.Date) {
			result.MissingSnapshots++
			continue
		}
		commissionPayable, petrolPayable := effectiveTripPayables(trip)
		for _, item := range []struct {
			component Component
			bucket    SettlementBucket
			amount    *float64
		}{{ComponentTripCommission, BucketCommission, commissionPayable}, {ComponentPetrol, BucketPetrol, petrolPayable}} {
			if item.amount == nil || !validMoney(*item.amount) {
				result.MissingSnapshots++
				continue
			}
			addReconciliationAmount(expected, workerID, workerType, item.component, item.bucket, moneyToPaise(*item.amount))
		}
	}

	beauticianScores := make([]leaderboard.BeauticianScore, 0, len(beauticians))
	seenBeauticians := map[primitive.ObjectID]struct{}{}
	for _, row := range beauticians {
		if row.WorkerID.IsZero() || !validMoney(row.Revenue) || row.OrderCount < 0 {
			return result, errors.New("invalid beautician leaderboard aggregate")
		}
		if _, exists := seenBeauticians[row.WorkerID]; exists {
			return result, errors.New("duplicate beautician leaderboard aggregate")
		}
		seenBeauticians[row.WorkerID] = struct{}{}
		beauticianScores = append(beauticianScores, leaderboard.BeauticianScore{WorkerID: row.WorkerID, Revenue: row.Revenue, OrderCount: row.OrderCount})
	}
	for _, award := range leaderboard.RankBeauticians(beauticianScores, prizes.Beautician) {
		addReconciliationAmount(expected, award.WorkerID, "beautician", ComponentLeaderboardBonus, BucketCommission, moneyToPaise(award.Bonus), "beautician_leaderboard")
	}
	riderScores := make([]leaderboard.RiderScore, 0, len(riders))
	riderTypes := map[primitive.ObjectID]string{}
	for _, row := range riders {
		if row.WorkerID.IsZero() || (row.WorkerType != "rider" && row.WorkerType != "beautician") || row.TripCount < 0 || !validMoney(row.TotalDistanceKM) {
			return result, errors.New("invalid rider leaderboard aggregate")
		}
		if _, exists := riderTypes[row.WorkerID]; exists {
			return result, errors.New("duplicate rider leaderboard aggregate")
		}
		riderTypes[row.WorkerID] = row.WorkerType
		riderScores = append(riderScores, leaderboard.RiderScore{WorkerID: row.WorkerID, TripCount: row.TripCount, TotalDistanceKM: row.TotalDistanceKM})
	}
	for _, award := range leaderboard.RankRiders(riderScores, prizes.Rider) {
		addReconciliationAmount(expected, award.WorkerID, riderTypes[award.WorkerID], ComponentLeaderboardBonus, BucketCommission, moneyToPaise(award.Bonus), "rider_leaderboard")
	}

	actual := map[reconciliationKey]int64{}
	targetMonthEnd := monthEndDate(endDate)
	for _, entry := range ledgerEntries {
		scope, included := reconciliationEntryScope(entry, startDate, endDate, targetMonth, targetMonthEnd)
		if !included {
			continue
		}
		addReconciliationAmount(actual, entry.WorkerID, entry.WorkerType, entry.Component, entry.SettlementBucket, entry.AmountPaise, scope)
	}
	keys := map[reconciliationKey]struct{}{}
	for key := range expected {
		keys[key] = struct{}{}
	}
	for key := range actual {
		keys[key] = struct{}{}
	}
	for key := range keys {
		difference := actual[key] - expected[key]
		status := "matched"
		if difference != 0 {
			status = "mismatched"
			result.Mismatched++
			result.AbsoluteDifferencePaise += absPaise(difference)
		} else {
			result.Matched++
		}
		result.ExpectedPaise += expected[key]
		result.LedgerPaise += actual[key]
		result.Rows = append(result.Rows, ReconciliationRow{WorkerID: key.workerID, WorkerType: key.workerType, Scope: key.scope, Component: key.component, Bucket: key.bucket, ExpectedPaise: expected[key], LedgerPaise: actual[key], DifferencePaise: difference, Status: status})
	}
	result.DifferencePaise = result.LedgerPaise - result.ExpectedPaise
	result.Ready = result.Mismatched == 0 && result.MissingSnapshots == 0
	sort.Slice(result.Rows, func(i, j int) bool {
		left, right := result.Rows[i], result.Rows[j]
		if left.Status != right.Status {
			return left.Status == "mismatched"
		}
		if left.WorkerID != right.WorkerID {
			return left.WorkerID.Hex() < right.WorkerID.Hex()
		}
		if left.Scope != right.Scope {
			return left.Scope < right.Scope
		}
		return left.Component < right.Component
	})
	return result, nil
}

func addReconciliationAmount(values map[reconciliationKey]int64, workerID primitive.ObjectID, workerType string, component Component, bucket SettlementBucket, amount int64, scopes ...string) {
	if amount == 0 {
		return
	}
	scope := reconciliationDefaultScope(component)
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	values[reconciliationKey{workerID: workerID, workerType: workerType, scope: scope, component: component, bucket: bucket}] += amount
}

func reconciliationEntryScope(entry LedgerEntry, startDate, endDate, targetMonth, targetMonthEnd string) (string, bool) {
	if entry.Status == StatusVoid || entry.WorkerID.IsZero() || (entry.WorkerType != "beautician" && entry.WorkerType != "rider") {
		return "", false
	}
	switch entry.Component {
	case ComponentSpecialCommission, ComponentGeneralCommission, ComponentUpgradeCommission:
		return "orders", entry.SourceType == "orders" && entry.SourceID != nil && entry.ServiceDateKey >= startDate && entry.ServiceDateKey <= endDate
	case ComponentTripCommission, ComponentPetrol:
		return "trips", entry.SourceType == "trips" && entry.SourceID != nil && entry.ServiceDateKey >= startDate && entry.ServiceDateKey <= endDate
	case ComponentTargetBonus:
		return "targets", entry.SourceType == "targets" && entry.ServiceDateKey == targetMonthEnd && entry.ConfigurationSnapshot["target_month"] == targetMonth
	case ComponentLeaderboardBonus:
		leaderboardType, ok := entry.ConfigurationSnapshot["leaderboard_type"].(string)
		return leaderboardType + "_leaderboard", ok && (leaderboardType == "beautician" || leaderboardType == "rider") && entry.SourceType == "leaderboards" && entry.ConfigurationSnapshot["period_start"] == startDate && entry.ConfigurationSnapshot["period_end"] == endDate
	default:
		return "", false
	}
}

func reconciliationDefaultScope(component Component) string {
	switch component {
	case ComponentSpecialCommission, ComponentGeneralCommission, ComponentUpgradeCommission:
		return "orders"
	case ComponentTripCommission, ComponentPetrol:
		return "trips"
	case ComponentTargetBonus:
		return "targets"
	default:
		return ""
	}
}

func absPaise(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

package earnings

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type mockStore struct {
	status                  map[string]interface{}
	statusErr               error
	entries                 []LedgerEntry
	entriesTotal            int64
	entriesErr              error
	summary                 []SummaryRow
	summaryErr              error
	reconciliation          ReconciliationResult
	reconciliationErr       error
	adjustment              LedgerEntry
	adjustmentCreated       bool
	adjustmentErr           error
	officeExists            bool
	officeErr               error
	activeStaff             bool
	activeStaffErr          error
	workerExists            bool
	workerErr               error
	dateClosed              bool
	dateClosedErr           error
	closedOverlap           bool
	closedOverlapErr        error
	activeRebuild           bool
	activeRebuildErr        error
	period                  Period
	periodCreated           bool
	periodErr               error
	rebuild                 RebuildJob
	rebuildCreated          bool
	rebuildErr              error
	rebuilds                []RebuildJob
	rebuildsTotal           int64
	rebuildsErr             error
	lastFilter              LedgerFilter
	lastAdjustment          LedgerEntry
	lastPeriod              Period
	lastRebuild             RebuildJob
	settlement              Settlement
	settlementCreated       bool
	settlementErr           error
	lastSettlement          Settlement
	existingSettlement      Settlement
	existingSettlementFound bool
	existingSettlementErr   error
	settlements             []Settlement
	settlementsTotal        int64
	settlementsErr          error
	lastSettlementFilter    SettlementFilter
	modeState               ModeState
	modeStateErr            error
	modeChanged             bool
	setModeErr              error
	lastMode                string
	lastModeReason          string
	reportDetail            ReportDetail
	reportDetailErr         error
}

func newMockStore() *mockStore {
	return &mockStore{
		status: map[string]interface{}{"ledger_entries": int64(1)}, officeExists: true,
		activeStaff: true, workerExists: true, adjustmentCreated: true,
		periodCreated: true, rebuildCreated: true, settlementCreated: true,
		modeChanged: true,
	}
}

func (m *mockStore) Status(context.Context, string) (map[string]interface{}, error) {
	if m.status == nil {
		m.status = map[string]interface{}{}
	}
	return m.status, m.statusErr
}
func (m *mockStore) ListEntries(_ context.Context, filter LedgerFilter) ([]LedgerEntry, int64, error) {
	m.lastFilter = filter
	return m.entries, m.entriesTotal, m.entriesErr
}
func (m *mockStore) Summary(context.Context, string, string, string) ([]SummaryRow, error) {
	return m.summary, m.summaryErr
}
func (m *mockStore) Reconcile(context.Context, primitive.ObjectID, string, string) (ReconciliationResult, error) {
	return m.reconciliation, m.reconciliationErr
}
func (m *mockStore) CreateAdjustment(_ context.Context, entry LedgerEntry) (LedgerEntry, bool, error) {
	m.lastAdjustment = entry
	if m.adjustment.ID.IsZero() {
		m.adjustment = entry
	}
	return m.adjustment, m.adjustmentCreated, m.adjustmentErr
}
func (m *mockStore) OfficeExists(context.Context, primitive.ObjectID) (bool, error) {
	return m.officeExists, m.officeErr
}
func (m *mockStore) ActiveStaffExists(context.Context, primitive.ObjectID) (bool, error) {
	return m.activeStaff, m.activeStaffErr
}
func (m *mockStore) WorkerBelongsToOffice(context.Context, string, primitive.ObjectID, primitive.ObjectID) (bool, error) {
	return m.workerExists, m.workerErr
}
func (m *mockStore) IsDateClosed(context.Context, primitive.ObjectID, string) (bool, error) {
	return m.dateClosed, m.dateClosedErr
}
func (m *mockStore) HasClosedPeriodOverlap(context.Context, primitive.ObjectID, string, string) (bool, error) {
	return m.closedOverlap, m.closedOverlapErr
}
func (m *mockStore) HasActiveRebuildOverlap(context.Context, primitive.ObjectID, string, string) (bool, error) {
	return m.activeRebuild, m.activeRebuildErr
}
func (m *mockStore) ClosePeriod(_ context.Context, period Period) (Period, bool, error) {
	m.lastPeriod = period
	if m.period.ID.IsZero() {
		m.period = period
	}
	return m.period, m.periodCreated, m.periodErr
}
func (m *mockStore) QueueRebuild(_ context.Context, job RebuildJob) (RebuildJob, bool, error) {
	m.lastRebuild = job
	if m.rebuild.ID.IsZero() {
		m.rebuild = job
	}
	return m.rebuild, m.rebuildCreated, m.rebuildErr
}
func (m *mockStore) ListRebuilds(_ context.Context, _ RebuildFilter) ([]RebuildJob, int64, error) {
	return m.rebuilds, m.rebuildsTotal, m.rebuildsErr
}
func (m *mockStore) AllocateSettlement(_ context.Context, settlement Settlement) (Settlement, bool, error) {
	m.lastSettlement = settlement
	if m.settlement.ID.IsZero() {
		m.settlement = settlement
	}
	return m.settlement, m.settlementCreated, m.settlementErr
}
func (m *mockStore) FindSettlement(_ context.Context, _ primitive.ObjectID, _ string) (Settlement, bool, error) {
	return m.existingSettlement, m.existingSettlementFound, m.existingSettlementErr
}
func (m *mockStore) ListSettlements(_ context.Context, filter SettlementFilter) ([]Settlement, int64, error) {
	m.lastSettlementFilter = filter
	return m.settlements, m.settlementsTotal, m.settlementsErr
}
func (m *mockStore) GetModeState(context.Context, primitive.ObjectID) (ModeState, error) {
	return m.modeState, m.modeStateErr
}
func (m *mockStore) SetMode(_ context.Context, officeID primitive.ObjectID, mode string, _ primitive.ObjectID, reason, _, _ string) (ModeState, bool, error) {
	m.lastMode, m.lastModeReason = mode, reason
	if m.setModeErr != nil {
		return ModeState{}, false, m.setModeErr
	}
	m.modeState.OfficeID, m.modeState.Mode = officeID, mode
	return m.modeState, m.modeChanged, nil
}
func (m *mockStore) LoadReportDetail(context.Context, primitive.ObjectID, primitive.ObjectID, string, string, string) (ReportDetail, error) {
	return m.reportDetail, m.reportDetailErr
}

var errStore = errors.New("store failure")

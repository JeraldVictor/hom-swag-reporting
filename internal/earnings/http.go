package earnings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var dateKeyPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type API struct {
	repo      Store
	jwtSecret string
	mode      string
}

type Store interface {
	Status(context.Context, string) (map[string]interface{}, error)
	ListEntries(context.Context, LedgerFilter) ([]LedgerEntry, int64, error)
	Summary(context.Context, string, string, string) ([]SummaryRow, error)
	Reconcile(context.Context, primitive.ObjectID, string, string) (ReconciliationResult, error)
	CreateAdjustment(context.Context, LedgerEntry) (LedgerEntry, bool, error)
	OfficeExists(context.Context, primitive.ObjectID) (bool, error)
	ActiveStaffExists(context.Context, primitive.ObjectID) (bool, error)
	WorkerBelongsToOffice(context.Context, string, primitive.ObjectID, primitive.ObjectID) (bool, error)
	IsDateClosed(context.Context, primitive.ObjectID, string) (bool, error)
	HasClosedPeriodOverlap(context.Context, primitive.ObjectID, string, string) (bool, error)
	HasActiveRebuildOverlap(context.Context, primitive.ObjectID, string, string) (bool, error)
	ClosePeriod(context.Context, Period) (Period, bool, error)
	QueueRebuild(context.Context, RebuildJob) (RebuildJob, bool, error)
	ListRebuilds(context.Context, RebuildFilter) ([]RebuildJob, int64, error)
	AllocateSettlement(context.Context, Settlement) (Settlement, bool, error)
	FindSettlement(context.Context, primitive.ObjectID, string) (Settlement, bool, error)
	ListSettlements(context.Context, SettlementFilter) ([]Settlement, int64, error)
	GetModeState(context.Context, primitive.ObjectID) (ModeState, error)
	SetMode(context.Context, primitive.ObjectID, string, primitive.ObjectID, string, string, string) (ModeState, bool, error)
}

func NewAPI(repo Store, jwtSecret, mode string) *API {
	if mode != ModeAuthoritative {
		mode = ModeShadow
	}
	return &API{repo: repo, jwtSecret: jwtSecret, mode: mode}
}

func (a *API) Handler() http.Handler { return http.HandlerFunc(a.serveHTTP) }

func (a *API) serveHTTP(w http.ResponseWriter, r *http.Request) {
	principal, err := VerifyAdminToken(r.Header.Get("Authorization"), a.jwtSecret)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if err := validateObjectID("JWT subject", principal.StaffID); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	officeID := requestOfficeID(r)
	if err := validateObjectID("office_id", officeID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := principal.CanAccessOffice(officeID); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/earnings")
	switch {
	case r.Method == http.MethodGet && path == "/status":
		a.require(w, principal, "ledger.read", func() { a.getStatus(w, r, officeID) })
	case r.Method == http.MethodGet && path == "/ledger":
		a.require(w, principal, "ledger.read", func() { a.listLedger(w, r, officeID) })
	case r.Method == http.MethodGet && path == "/report-detail":
		a.require(w, principal, "ledger.read", func() { a.getReportDetail(w, r, officeID) })
	case r.Method == http.MethodGet && path == "/summary":
		a.require(w, principal, "ledger.read", func() { a.getSummary(w, r, officeID) })
	case r.Method == http.MethodGet && path == "/reconciliation":
		a.require(w, principal, "ledger.read", func() { a.getReconciliation(w, r, officeID) })
	case r.Method == http.MethodPost && path == "/mode":
		a.require(w, principal, "ledger.cutover", func() { a.changeMode(w, r, principal, officeID) })
	case r.Method == http.MethodPost && path == "/adjustments":
		a.require(w, principal, "ledger.payout", func() { a.createAdjustment(w, r, principal, officeID) })
	case r.Method == http.MethodPost && path == "/settlements":
		a.require(w, principal, "ledger.payout", func() { a.createSettlement(w, r, principal, officeID) })
	case r.Method == http.MethodGet && path == "/settlements":
		a.require(w, principal, "ledger.read", func() { a.listSettlements(w, r, officeID) })
	case r.Method == http.MethodPost && path == "/periods/close":
		a.require(w, principal, "ledger.payout", func() { a.closePeriod(w, r, principal, officeID) })
	case r.Method == http.MethodPost && path == "/rebuilds":
		a.require(w, principal, "ledger.rebuild", func() { a.queueRebuild(w, r, principal, officeID) })
	case r.Method == http.MethodGet && path == "/rebuilds":
		a.require(w, principal, "ledger.rebuild", func() { a.listRebuilds(w, r, officeID) })
	default:
		writeError(w, http.StatusNotFound, errors.New("earnings endpoint not found"))
	}
}

func (a *API) listSettlements(w http.ResponseWriter, r *http.Request, officeID string) {
	workerID := strings.TrimSpace(r.URL.Query().Get("worker_id"))
	if workerID != "" {
		if err := validateObjectID("worker_id", workerID); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
	if bucket != "" && bucket != string(BucketCommission) && bucket != string(BucketPetrol) {
		writeError(w, http.StatusBadRequest, errors.New("bucket must be commission or petrol"))
		return
	}
	startDate, endDate := r.URL.Query().Get("start_date"), r.URL.Query().Get("end_date")
	if (startDate == "") != (endDate == "") {
		writeError(w, http.StatusBadRequest, errors.New("start_date and end_date must be provided together"))
		return
	}
	if startDate != "" {
		if err := validateDateRange(startDate, endDate); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	page, limit := boundedPagination(r)
	settlements, total, err := a.repo.ListSettlements(r.Context(), SettlementFilter{
		OfficeID: officeID, WorkerID: workerID, Bucket: bucket, StartDate: startDate, EndDate: endDate, Page: page, Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if settlements == nil {
		settlements = make([]Settlement, 0)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"settlements": settlements, "total": total, "page": page, "limit": limit})
}

type settlementRequest struct {
	WorkerID       string           `json:"worker_id"`
	WorkerType     string           `json:"worker_type"`
	Bucket         SettlementBucket `json:"bucket"`
	StartDate      string           `json:"start_date"`
	EndDate        string           `json:"end_date"`
	AmountPaise    int64            `json:"amount_paise"`
	PaymentMethod  string           `json:"payment_method"`
	Reference      string           `json:"reference"`
	Remarks        string           `json:"remarks"`
	IdempotencyKey string           `json:"idempotency_key"`
}

func (a *API) createSettlement(w http.ResponseWriter, r *http.Request, principal Principal, officeID string) {
	var input settlementRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateObjectID("worker_id", input.WorkerID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if input.WorkerType != "beautician" && input.WorkerType != "rider" {
		writeError(w, http.StatusBadRequest, errors.New("worker_type must be beautician or rider"))
		return
	}
	if input.Bucket != BucketCommission && input.Bucket != BucketPetrol {
		writeError(w, http.StatusBadRequest, errors.New("bucket must be commission or petrol"))
		return
	}
	if err := validateDateRange(input.StartDate, input.EndDate); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if input.AmountPaise <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("amount_paise must be greater than zero"))
		return
	}
	validMethods := map[string]bool{"cash": true, "bank_transfer": true, "upi": true, "other": true}
	if !validMethods[input.PaymentMethod] {
		writeError(w, http.StatusBadRequest, errors.New("payment_method must be cash, bank_transfer, upi, or other"))
		return
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Reference, input.Remarks = strings.TrimSpace(input.Reference), strings.TrimSpace(input.Remarks)
	if input.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, errors.New("idempotency_key is required"))
		return
	}
	if len(input.IdempotencyKey) > 200 || len(input.Reference) > 200 || len(input.Remarks) > 500 {
		writeError(w, http.StatusBadRequest, errors.New("idempotency_key and reference must be at most 200 characters; remarks at most 500"))
		return
	}
	if !a.requireActiveStaff(w, r, principal.StaffID) || !a.requireExistingOffice(w, r, officeID) {
		return
	}
	officeObjectID, workerObjectID := mustObjectID(officeID), mustObjectID(input.WorkerID)
	existing, found, err := a.repo.FindSettlement(r.Context(), officeObjectID, input.IdempotencyKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if found {
		writeJSON(w, http.StatusOK, map[string]interface{}{"settlement": existing, "created": false})
		return
	}
	workerExists, err := a.repo.WorkerBelongsToOffice(r.Context(), input.WorkerType, workerObjectID, officeObjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !workerExists {
		writeError(w, http.StatusUnprocessableEntity, errors.New("worker was not found in the selected office"))
		return
	}
	closed, err := a.repo.HasClosedPeriodOverlap(r.Context(), officeObjectID, input.StartDate, input.EndDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if closed {
		writeError(w, http.StatusConflict, errors.New("the settlement range overlaps a closed earning period"))
		return
	}
	activeRebuild, err := a.repo.HasActiveRebuildOverlap(r.Context(), officeObjectID, input.StartDate, input.EndDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if activeRebuild {
		writeError(w, http.StatusConflict, errors.New("an overlapping rebuild is queued or running"))
		return
	}
	settlement, created, err := a.repo.AllocateSettlement(r.Context(), Settlement{
		OfficeID: officeObjectID, WorkerID: workerObjectID, WorkerType: input.WorkerType, Bucket: input.Bucket,
		StartDate: input.StartDate, EndDate: input.EndDate, AmountPaise: input.AmountPaise,
		PaymentMethod: input.PaymentMethod, Reference: input.Reference, Remarks: input.Remarks,
		IdempotencyKey: input.IdempotencyKey, CreatedBy: mustObjectID(principal.StaffID),
	})
	if err != nil {
		if errors.Is(err, ErrNoPendingEarnings) || errors.Is(err, ErrSettlementExceedsPending) {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]interface{}{"settlement": settlement, "created": created})
}

func (a *API) listRebuilds(w http.ResponseWriter, r *http.Request, officeID string) {
	page, limit := boundedPagination(r)
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" {
		valid := map[string]bool{"queued": true, "running": true, "completed": true, "completed_with_issues": true, "failed": true}
		if !valid[status] {
			writeError(w, http.StatusBadRequest, errors.New("status must be queued, running, completed, completed_with_issues, or failed"))
			return
		}
	}
	jobs, total, err := a.repo.ListRebuilds(r.Context(), RebuildFilter{OfficeID: officeID, Status: status, Page: page, Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"jobs": jobs, "total": total, "page": page, "limit": limit})
}

func (a *API) require(w http.ResponseWriter, principal Principal, permission string, fn func()) {
	if !principal.HasPermission(permission) {
		writeError(w, http.StatusForbidden, errors.New("missing permission: "+permission))
		return
	}
	fn()
}

func (a *API) getStatus(w http.ResponseWriter, r *http.Request, officeID string) {
	status, err := a.repo.Status(r.Context(), officeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	modeState, err := a.repo.GetModeState(r.Context(), mustObjectID(officeID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	mode := modeState.Mode
	if mode == "" {
		mode = a.mode
	}
	status["mode"], status["authoritative"] = mode, mode == ModeAuthoritative
	status["mode_updated_by"], status["mode_updated_at"] = modeState.UpdatedBy, modeState.UpdatedAt
	writeJSON(w, http.StatusOK, status)
}

type modeChangeRequest struct {
	Mode      string `json:"mode"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Reason    string `json:"reason"`
}

func (a *API) changeMode(w http.ResponseWriter, r *http.Request, principal Principal, officeID string) {
	var input modeChangeRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	input.Mode, input.Reason = strings.TrimSpace(input.Mode), strings.TrimSpace(input.Reason)
	if input.Mode != ModeShadow && input.Mode != ModeAuthoritative {
		writeError(w, http.StatusBadRequest, errors.New("mode must be shadow or authoritative"))
		return
	}
	if input.Reason == "" || len(input.Reason) > 500 {
		writeError(w, http.StatusBadRequest, errors.New("reason is required and must be at most 500 characters"))
		return
	}
	if !a.requireActiveStaff(w, r, principal.StaffID) || !a.requireExistingOffice(w, r, officeID) {
		return
	}
	officeObjectID := mustObjectID(officeID)
	current, err := a.repo.GetModeState(r.Context(), officeObjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if current.Mode == input.Mode {
		writeJSON(w, http.StatusOK, map[string]interface{}{"state": current, "changed": false})
		return
	}

	var reconciliation *ReconciliationResult
	reconciliationFrom, reconciliationTo := "", ""
	if input.Mode == ModeAuthoritative {
		if err := validateDateRange(input.StartDate, input.EndDate); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		active, err := a.repo.HasActiveRebuildOverlap(r.Context(), officeObjectID, input.StartDate, input.EndDate)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if active {
			writeError(w, http.StatusConflict, errors.New("an overlapping rebuild is queued or running"))
			return
		}
		result, err := a.repo.Reconcile(r.Context(), officeObjectID, input.StartDate, input.EndDate)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		reconciliation = &result
		if !result.Ready {
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"message":        "reconciliation is not ready for authoritative cutover",
				"reconciliation": result,
			})
			return
		}
		reconciliationFrom, reconciliationTo = input.StartDate, input.EndDate
	}

	state, changed, err := a.repo.SetMode(
		r.Context(), officeObjectID, input.Mode, mustObjectID(principal.StaffID), input.Reason,
		reconciliationFrom, reconciliationTo,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"state": state, "changed": changed, "reconciliation": reconciliation,
	})
}

func (a *API) listLedger(w http.ResponseWriter, r *http.Request, officeID string) {
	page, limit := boundedPagination(r)
	workerID := r.URL.Query().Get("worker_id")
	if workerID != "" {
		if err := validateObjectID("worker_id", workerID); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	startDate, endDate := r.URL.Query().Get("start_date"), r.URL.Query().Get("end_date")
	if err := validateDateRange(startDate, endDate); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entries, total, err := a.repo.ListEntries(r.Context(), LedgerFilter{
		OfficeID: officeID, WorkerID: workerID, Component: r.URL.Query().Get("component"),
		Bucket: r.URL.Query().Get("bucket"), Status: r.URL.Query().Get("status"),
		StartDate: startDate, EndDate: endDate, Page: page, Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if entries == nil {
		entries = make([]LedgerEntry, 0)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"entries": entries, "total": total, "page": page, "limit": limit})
}

func (a *API) getSummary(w http.ResponseWriter, r *http.Request, officeID string) {
	startDate, endDate := r.URL.Query().Get("start_date"), r.URL.Query().Get("end_date")
	if err := validateDateRange(startDate, endDate); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rows, err := a.repo.Summary(r.Context(), officeID, startDate, endDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var earned, settled int64
	for _, row := range rows {
		earned += row.AmountPaise
		settled += row.SettledAmountPaise
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"components": rows, "earned_paise": earned, "settled_paise": settled, "pending_paise": earned - settled,
	})
}

func (a *API) getReconciliation(w http.ResponseWriter, r *http.Request, officeID string) {
	startDate, endDate := r.URL.Query().Get("start_date"), r.URL.Query().Get("end_date")
	if err := validateDateRange(startDate, endDate); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := a.repo.Reconcile(r.Context(), mustObjectID(officeID), startDate, endDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type adjustmentRequest struct {
	WorkerID       string           `json:"worker_id"`
	WorkerType     string           `json:"worker_type"`
	Bucket         SettlementBucket `json:"bucket"`
	AmountPaise    int64            `json:"amount_paise"`
	ServiceDate    string           `json:"service_date"`
	Reason         string           `json:"reason"`
	IdempotencyKey string           `json:"idempotency_key"`
}

func (a *API) createAdjustment(w http.ResponseWriter, r *http.Request, principal Principal, officeID string) {
	var input adjustmentRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateObjectID("worker_id", input.WorkerID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if input.WorkerType != "beautician" && input.WorkerType != "rider" {
		writeError(w, http.StatusBadRequest, errors.New("worker_type must be beautician or rider"))
		return
	}
	if input.Bucket != BucketCommission && input.Bucket != BucketPetrol {
		writeError(w, http.StatusBadRequest, errors.New("bucket must be commission or petrol"))
		return
	}
	if !validDateKey(input.ServiceDate) {
		writeError(w, http.StatusBadRequest, errors.New("service_date must be YYYY-MM-DD"))
		return
	}
	if strings.TrimSpace(input.Reason) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		writeError(w, http.StatusBadRequest, errors.New("reason and idempotency_key are required"))
		return
	}
	if len(strings.TrimSpace(input.Reason)) > 500 || len(strings.TrimSpace(input.IdempotencyKey)) > 200 {
		writeError(w, http.StatusBadRequest, errors.New("reason must be at most 500 characters and idempotency_key at most 200 characters"))
		return
	}
	if input.AmountPaise == 0 {
		writeError(w, http.StatusBadRequest, errors.New("adjustment amount cannot be zero"))
		return
	}
	if !a.requireActiveStaff(w, r, principal.StaffID) {
		return
	}
	officeObjectID := mustObjectID(officeID)
	workerObjectID := mustObjectID(input.WorkerID)
	officeExists, err := a.repo.OfficeExists(r.Context(), officeObjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !officeExists {
		writeError(w, http.StatusNotFound, errors.New("office was not found"))
		return
	}
	workerExists, err := a.repo.WorkerBelongsToOffice(r.Context(), input.WorkerType, workerObjectID, officeObjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !workerExists {
		writeError(w, http.StatusUnprocessableEntity, errors.New("worker was not found in the selected office"))
		return
	}
	closed, err := a.repo.IsDateClosed(r.Context(), officeObjectID, input.ServiceDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if closed {
		writeError(w, http.StatusConflict, errors.New("the earning period containing service_date is closed; post the correction in an open period"))
		return
	}
	component := ComponentCommissionAdjustment
	if input.Bucket == BucketPetrol {
		component = ComponentPetrolAdjustment
	}
	entry, created, err := a.repo.CreateAdjustment(r.Context(), LedgerEntry{
		OfficeID: officeObjectID, WorkerID: workerObjectID, WorkerType: input.WorkerType,
		ServiceDateKey: input.ServiceDate, Component: component, SettlementBucket: input.Bucket,
		AmountPaise: input.AmountPaise, Status: StatusOpen, SourceType: "manual", CalculationVersion: 1,
		Reason: strings.TrimSpace(input.Reason), IdempotencyKey: strings.TrimSpace(input.IdempotencyKey), CreatedBy: mustObjectID(principal.StaffID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]interface{}{"entry": entry, "created": created})
}

func (a *API) closePeriod(w http.ResponseWriter, r *http.Request, principal Principal, officeID string) {
	var input struct {
		Kind      string `json:"kind"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if input.Kind != "weekly" && input.Kind != "monthly" {
		writeError(w, http.StatusBadRequest, errors.New("kind must be weekly or monthly"))
		return
	}
	if err := validateDateRange(input.StartDate, input.EndDate); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !a.requireActiveStaff(w, r, principal.StaffID) {
		return
	}
	if !a.requireExistingOffice(w, r, officeID) {
		return
	}
	activeRebuild, err := a.repo.HasActiveRebuildOverlap(r.Context(), mustObjectID(officeID), input.StartDate, input.EndDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if activeRebuild {
		writeError(w, http.StatusConflict, errors.New("an overlapping rebuild is queued or running; close the period after it finishes"))
		return
	}
	period, created, err := a.repo.ClosePeriod(r.Context(), Period{OfficeID: mustObjectID(officeID), Kind: input.Kind, StartDate: input.StartDate, EndDate: input.EndDate, ClosedBy: mustObjectID(principal.StaffID)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]interface{}{"period": period, "created": created})
}

func (a *API) queueRebuild(w http.ResponseWriter, r *http.Request, principal Principal, officeID string) {
	var input struct {
		StartDate      string `json:"start_date"`
		EndDate        string `json:"end_date"`
		Scope          string `json:"scope"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateDateRange(input.StartDate, input.EndDate); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if input.Scope != "all" && input.Scope != "commissions" && input.Scope != "petrol" && input.Scope != "leaderboards" {
		writeError(w, http.StatusBadRequest, errors.New("scope must be all, commissions, petrol, or leaderboards"))
		return
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		writeError(w, http.StatusBadRequest, errors.New("idempotency_key is required"))
		return
	}
	if !a.requireActiveStaff(w, r, principal.StaffID) {
		return
	}
	if !a.requireExistingOffice(w, r, officeID) {
		return
	}
	closedOverlap, err := a.repo.HasClosedPeriodOverlap(r.Context(), mustObjectID(officeID), input.StartDate, input.EndDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if closedOverlap {
		writeError(w, http.StatusConflict, errors.New("the rebuild range overlaps a closed earning period"))
		return
	}
	job, created, err := a.repo.QueueRebuild(r.Context(), RebuildJob{OfficeID: mustObjectID(officeID), StartDate: input.StartDate, EndDate: input.EndDate, Scope: input.Scope, IdempotencyKey: input.IdempotencyKey, RequestedBy: mustObjectID(principal.StaffID)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]interface{}{"job": job, "created": created})
}

func (a *API) requireExistingOffice(w http.ResponseWriter, r *http.Request, officeID string) bool {
	exists, err := a.repo.OfficeExists(r.Context(), mustObjectID(officeID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return false
	}
	if !exists {
		writeError(w, http.StatusNotFound, errors.New("office was not found"))
		return false
	}
	return true
}

func (a *API) requireActiveStaff(w http.ResponseWriter, r *http.Request, staffID string) bool {
	exists, err := a.repo.ActiveStaffExists(r.Context(), mustObjectID(staffID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return false
	}
	if !exists {
		writeError(w, http.StatusForbidden, errors.New("the authenticated staff account is inactive or deleted"))
		return false
	}
	return true
}

func requestOfficeID(r *http.Request) string {
	if value := r.URL.Query().Get("office_id"); value != "" {
		return value
	}
	return r.Header.Get("X-Office-ID")
}
func validateObjectID(name, value string) error {
	if !primitive.IsValidObjectID(value) {
		return errors.New(name + " must be a valid ObjectID")
	}
	return nil
}
func validateDateRange(start, end string) error {
	if !validDateKey(start) || !validDateKey(end) {
		return errors.New("start_date and end_date must be YYYY-MM-DD")
	}
	if start > end {
		return errors.New("start_date cannot be after end_date")
	}
	return nil
}
func validDateKey(value string) bool {
	if !dateKeyPattern.MatchString(value) {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}
func boundedPagination(r *http.Request) (int64, int64) {
	page, _ := strconv.ParseInt(r.URL.Query().Get("page"), 10, 64)
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return page, limit
}
func decodeJSON(r *http.Request, target interface{}) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": http.StatusText(status), "message": err.Error()})
}

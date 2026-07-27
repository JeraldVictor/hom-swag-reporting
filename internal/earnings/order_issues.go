package earnings

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	OrderIssuePaymentMismatch   = "payment_total_mismatch"
	OrderIssueMissingPayment    = "missing_payment"
	OrderIssueInvalidTotal      = "order_total_formula_mismatch"
	OrderIssueMissingBeautician = "missing_beautician_assignment"
	OrderIssueDeletedBeautician = "deleted_beautician_profile"
	OrderIssueMissingCommission = "missing_commission_snapshot"
	OrderIssueInvalidCommission = "invalid_commission_snapshot"

	OrderIssueOpen     = "open"
	OrderIssueResolved = "resolved"
	OrderIssueAccepted = "accepted"

	OrderIssueActionRecheck = "recheck"
	OrderIssueActionAccept  = "accept_variance"
	OrderIssueActionAlign   = "align_payment_record"
)

var normalizedPaymentSeparator = regexp.MustCompile(`[-\s]+`)

type OrderIssueResolution struct {
	Action          string             `bson:"action" json:"action"`
	Reason          string             `bson:"reason,omitempty" json:"reason,omitempty"`
	ActorID         primitive.ObjectID `bson:"actor_id" json:"actor_id"`
	At              time.Time          `bson:"at" json:"at"`
	AdjustmentPaise int64              `bson:"adjustment_paise,omitempty" json:"adjustment_paise,omitempty"`
}

type OrderReconciliationIssue struct {
	ID                primitive.ObjectID     `bson:"_id,omitempty" json:"id"`
	IssueKey          string                 `bson:"issue_key" json:"issue_key"`
	OfficeID          primitive.ObjectID     `bson:"office_id" json:"office_id"`
	IssueType         string                 `bson:"issue_type" json:"issue_type"`
	Severity          string                 `bson:"severity" json:"severity"`
	Status            string                 `bson:"status" json:"status"`
	OrderID           primitive.ObjectID     `bson:"order_id" json:"order_id"`
	OrderNumber       string                 `bson:"order_number" json:"order_number"`
	ServiceDate       string                 `bson:"service_date" json:"service_date"`
	ExpectedPaise     int64                  `bson:"expected_paise" json:"expected_paise"`
	ActualPaise       int64                  `bson:"actual_paise" json:"actual_paise"`
	DifferencePaise   int64                  `bson:"difference_paise" json:"difference_paise"`
	PaymentMethod     string                 `bson:"payment_method,omitempty" json:"payment_method,omitempty"`
	Explanation       string                 `bson:"explanation" json:"explanation"`
	Details           map[string]interface{} `bson:"details,omitempty" json:"details,omitempty"`
	FirstDetectedAt   time.Time              `bson:"first_detected_at" json:"first_detected_at"`
	LastDetectedAt    time.Time              `bson:"last_detected_at" json:"last_detected_at"`
	ResolvedAt        *time.Time             `bson:"resolved_at,omitempty" json:"resolved_at,omitempty"`
	ResolvedBy        *primitive.ObjectID    `bson:"resolved_by,omitempty" json:"resolved_by,omitempty"`
	Resolution        *OrderIssueResolution  `bson:"resolution,omitempty" json:"resolution,omitempty"`
	ResolutionHistory []OrderIssueResolution `bson:"resolution_history,omitempty" json:"resolution_history,omitempty"`
}

type OrderIssueFilter struct {
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

type OrderIssueScanResult struct {
	Scanned       int64 `json:"scanned"`
	Open          int64 `json:"open"`
	Created       int64 `json:"created"`
	Updated       int64 `json:"updated"`
	AutoResolved  int64 `json:"auto_resolved"`
	TotalVariance int64 `json:"total_variance_paise"`
}

type OrderIssueActionInput struct {
	Action string
	Reason string
	Actor  primitive.ObjectID
}

type OrderIssueStore interface {
	ScanOrderIssues(context.Context, primitive.ObjectID, string, string) (OrderIssueScanResult, error)
	ListOrderIssues(context.Context, OrderIssueFilter) ([]OrderReconciliationIssue, int64, error)
	ActOnOrderIssue(context.Context, primitive.ObjectID, primitive.ObjectID, OrderIssueActionInput) (OrderReconciliationIssue, error)
}

var (
	ErrOrderIssueNotFound      = errors.New("order reconciliation issue was not found")
	ErrOrderIssueAlreadyClosed = errors.New("order reconciliation issue is already closed")
	ErrOrderIssueStillPresent  = errors.New("the order still has a reconciliation discrepancy")
	ErrOrderIssueUnsupported   = errors.New("this reconciliation action is not supported for the issue")
)

type orderIssuePaymentHistory struct {
	ID                    primitive.ObjectID  `bson:"_id,omitempty"`
	Label                 string              `bson:"label"`
	Method                string              `bson:"method"`
	Status                string              `bson:"status"`
	Amount                float64             `bson:"amount"`
	ReconciliationIssueID *primitive.ObjectID `bson:"reconciliation_issue_id,omitempty"`
}

type orderIssuePayment struct {
	Method             string                     `bson:"method"`
	Tip                float64                    `bson:"tip"`
	ActualPaidAmount   *float64                   `bson:"actual_paid_amount,omitempty"`
	AmountPaid         *float64                   `bson:"amount_paid,omitempty"`
	CODAmount          *float64                   `bson:"cod_amount,omitempty"`
	UPIAmount          *float64                   `bson:"upi_amount,omitempty"`
	OnlineAmount       *float64                   `bson:"online_amount,omitempty"`
	BankTransferAmount *float64                   `bson:"bank_transfer_amount,omitempty"`
	History            []orderIssuePaymentHistory `bson:"history"`
}

type orderIssueBooking struct {
	Date        string  `bson:"date"`
	SurgeAmount float64 `bson:"surge_amount"`
}

type orderIssueSource struct {
	ID                 primitive.ObjectID  `bson:"_id"`
	OfficeID           primitive.ObjectID  `bson:"office_id"`
	OrderNumber        string              `bson:"order_number"`
	BeauticianID       *primitive.ObjectID `bson:"beautician_id"`
	Status             string              `bson:"status"`
	IsDeleted          bool                `bson:"is_deleted"`
	Booking            orderIssueBooking   `bson:"booking_info"`
	Subtotal           float64             `bson:"subtotal"`
	ConvenienceFees    float64             `bson:"convenience_fees"`
	HygieneFees        float64             `bson:"hygiene_fees"`
	MembershipCharge   float64             `bson:"membership_charge"`
	DiscountTotal      float64             `bson:"discount_total"`
	Total              *float64            `bson:"total,omitempty"`
	Tip                float64             `bson:"tip"`
	CODCollectedAmount float64             `bson:"cod_collected_amount"`
	Payment            orderIssuePayment   `bson:"payment"`
	CommissionSnapshot *CommissionSnapshot `bson:"commission_snapshot"`
}

func detectOrderCommissionIssues(source orderIssueSource, beautician *tripIssueWorker, now time.Time) []OrderReconciliationIssue {
	issues := make([]OrderReconciliationIssue, 0)
	// Legacy orders without either commission-era field remain covered by the
	// payment reconciliation checks. Once either field exists, require the
	// complete commission attribution and snapshot contract.
	if source.BeauticianID == nil && source.CommissionSnapshot == nil {
		return issues
	}
	if source.BeauticianID == nil || source.BeauticianID.IsZero() {
		issues = append(issues, newOrderIssue(source, source.Booking.Date, OrderIssueMissingBeautician, "critical", 0, 0,
			"The completed order has no beautician assignment, so commission cannot be attributed.", now,
			map[string]interface{}{"recommended_fix": "Assign the correct beautician at source, then recheck the order."}))
	} else if beautician == nil {
		issues = append(issues, newOrderIssue(source, source.Booking.Date, OrderIssueMissingBeautician, "critical", 0, 0,
			"The assigned beautician profile does not exist in this office.", now,
			map[string]interface{}{"beautician_id": source.BeauticianID.Hex(), "recommended_fix": "Assign an active beautician from this office, then recheck."}))
	} else if beautician.IsDeleted {
		issues = append(issues, newOrderIssue(source, source.Booking.Date, OrderIssueDeletedBeautician, "critical", 0, 0,
			"The completed order is assigned to a deleted beautician profile.", now,
			map[string]interface{}{"beautician_id": beautician.ID.Hex(), "employee_code": beautician.EmpCode, "recommended_fix": "Review the employee identity, reassign the order to the active canonical profile, and recheck."}))
	}
	if source.CommissionSnapshot == nil {
		issues = append(issues, newOrderIssue(source, source.Booking.Date, OrderIssueMissingCommission, "critical", 0, 0,
			"The completed order has no commission snapshot, so its earnings cannot be independently audited.", now,
			map[string]interface{}{"recommended_fix": "Rebuild the order commission snapshot using the verified order services and commission rules."}))
		return issues
	}
	snapshot := source.CommissionSnapshot
	values := map[string]*float64{
		"order_cost": snapshot.OrderCost, "special_commission": snapshot.SpecialCommission,
		"general_commission": snapshot.GeneralCommission, "upgrade_addon_commission": snapshot.UpgradeAddonCommission,
	}
	invalid := make([]string, 0)
	for field, value := range values {
		if value == nil || math.IsNaN(pointerMoney(value)) || math.IsInf(pointerMoney(value), 0) || pointerMoney(value) < 0 {
			invalid = append(invalid, field)
		}
	}
	if len(invalid) > 0 {
		issues = append(issues, newOrderIssue(source, source.Booking.Date, OrderIssueInvalidCommission, "critical", 0, 0,
			"The order commission snapshot has missing, negative, or non-finite values.", now,
			map[string]interface{}{"invalid_fields": invalid, "recommended_fix": "Correct the source commission inputs and rebuild the order commission snapshot."}))
	}
	return issues
}

func detectOrderIssues(source orderIssueSource, now time.Time) []OrderReconciliationIssue {
	serviceDate := source.Booking.Date
	base := moneyToPaise(source.Subtotal + source.ConvenienceFees + source.HygieneFees +
		source.Booking.SurgeAmount + source.MembershipCharge - source.DiscountTotal)
	tip := moneyToPaise(effectiveOrderIssueTip(source))
	actual := orderIssueReceivedPaise(source)

	if source.Total == nil || !validMoney(*source.Total) {
		return []OrderReconciliationIssue{newOrderIssue(source, serviceDate, OrderIssueInvalidTotal, "critical", base, 0,
			"The completed order has a missing, negative, or non-finite total.", now, map[string]interface{}{"calculated_base_paise": base, "tip_paise": tip})}
	}
	total := moneyToPaise(*source.Total)
	expected := total
	totalModel := "tip_included"
	switch {
	case absPaise(total-(base+tip)) <= 1:
		expected = total
	case absPaise(total-base) <= 1:
		expected = total + tip
		totalModel = "tip_separate"
	default:
		return []OrderReconciliationIssue{newOrderIssue(source, serviceDate, OrderIssueInvalidTotal, orderIssueSeverity(total-base), base+tip, total,
			"Order total does not match either supported charge model (tip included or tip stored separately).", now,
			map[string]interface{}{"calculated_base_paise": base, "tip_paise": tip, "stored_total_paise": total})}
	}
	actual = orderIssueReceivedPaiseForExpected(source, expected)

	difference := actual - expected
	if difference == 0 {
		return nil
	}
	issueType := OrderIssuePaymentMismatch
	explanation := "Recorded payments do not equal the final order receivable."
	if actual == 0 && expected > 0 {
		issueType = OrderIssueMissingPayment
		explanation = "The order is completed but has no recorded payment."
	}
	return []OrderReconciliationIssue{newOrderIssue(source, serviceDate, issueType, orderIssueSeverity(difference), expected, actual,
		explanation, now, map[string]interface{}{"total_model": totalModel, "stored_total_paise": total, "tip_paise": tip})}
}

func newOrderIssue(source orderIssueSource, serviceDate, issueType, severity string, expected, actual int64, explanation string, now time.Time, details map[string]interface{}) OrderReconciliationIssue {
	return OrderReconciliationIssue{
		IssueKey: source.OfficeID.Hex() + ":" + source.ID.Hex() + ":" + issueType,
		OfficeID: source.OfficeID, IssueType: issueType, Severity: severity, Status: OrderIssueOpen,
		OrderID: source.ID, OrderNumber: source.OrderNumber, ServiceDate: serviceDate,
		ExpectedPaise: expected, ActualPaise: actual, DifferencePaise: actual - expected,
		PaymentMethod: normalizeOrderPaymentText(source.Payment.Method), Explanation: explanation, Details: details,
		FirstDetectedAt: now, LastDetectedAt: now, ResolutionHistory: []OrderIssueResolution{},
	}
}

func orderIssueSeverity(difference int64) string {
	amount := absPaise(difference)
	if amount >= 10_000 {
		return "critical"
	}
	if amount >= 1_000 {
		return "high"
	}
	return "warning"
}

func effectiveOrderIssueTip(source orderIssueSource) float64 {
	if validMoney(source.Payment.Tip) && source.Payment.Tip > 0 {
		return source.Payment.Tip
	}
	return source.Tip
}

func orderIssueReceivedPaise(source orderIssueSource) int64 {
	received, _ := orderIssueReceivedCandidates(source)
	return received
}

func orderIssueReceivedPaiseForExpected(source orderIssueSource, expected int64) int64 {
	received, separateTips := orderIssueReceivedCandidates(source)
	withSeparateTips := received + separateTips
	if separateTips > 0 && absPaise(withSeparateTips-expected) < absPaise(received-expected) {
		return withSeparateTips
	}
	return received
}

func orderIssueReceivedCandidates(source orderIssueSource) (int64, int64) {
	if len(source.Payment.History) > 0 {
		var received int64
		var separateTips int64
		for _, entry := range source.Payment.History {
			label := normalizeOrderPaymentText(entry.Label)
			method := normalizeOrderPaymentText(entry.Method)
			amount := moneyToPaise(entry.Amount)
			if label == "tip" || method == "tip" || method == "tips" {
				if amount > 0 {
					separateTips += amount
				}
				continue
			}
			if strings.Contains(label, "refund") || strings.Contains(label, "cancellation_fee") || strings.Contains(label, "cancellation_charge") {
				continue
			}
			if amount > 0 || label == "reconciliation_adjustment" {
				received += amount
			}
		}
		return received, separateTips
	}
	paid := firstOrderIssueMoney(source.Payment.ActualPaidAmount, source.Payment.AmountPaid)
	if paid > 0 {
		return moneyToPaise(paid), 0
	}
	cod := pointerMoney(source.Payment.CODAmount)
	if source.Payment.CODAmount == nil {
		cod = source.CODCollectedAmount
	}
	return moneyToPaise(cod + pointerMoney(source.Payment.UPIAmount) + pointerMoney(source.Payment.OnlineAmount) +
		pointerMoney(source.Payment.BankTransferAmount)), 0
}

func normalizeOrderPaymentText(value string) string {
	return normalizedPaymentSeparator.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "_")
}

func firstOrderIssueMoney(values ...*float64) float64 {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return 0
}

func pointerMoney(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func validOrderIssueSource(source orderIssueSource) bool {
	return !source.ID.IsZero() && !source.OfficeID.IsZero() && source.Status == "completed" && !source.IsDeleted &&
		validSourceDate(source.Booking.Date) && validMoney(source.Subtotal) && validMoney(source.ConvenienceFees) &&
		validMoney(source.HygieneFees) && validMoney(source.Booking.SurgeAmount) && validMoney(source.MembershipCharge) &&
		validMoney(source.DiscountTotal) && validMoney(effectiveOrderIssueTip(source))
}

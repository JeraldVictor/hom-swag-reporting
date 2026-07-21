package earnings

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type mockOrderIssueStore struct {
	*mockStore
	scanResult OrderIssueScanResult
	scanErr    error
	issues     []OrderReconciliationIssue
	total      int64
	listErr    error
	action     OrderReconciliationIssue
	actionErr  error
	lastFilter OrderIssueFilter
	lastAction OrderIssueActionInput
}

func newMockOrderIssueStore() *mockOrderIssueStore {
	return &mockOrderIssueStore{mockStore: newMockStore(), issues: []OrderReconciliationIssue{}}
}

func (m *mockOrderIssueStore) ScanOrderIssues(context.Context, primitive.ObjectID, string, string) (OrderIssueScanResult, error) {
	return m.scanResult, m.scanErr
}

func (m *mockOrderIssueStore) ListOrderIssues(_ context.Context, filter OrderIssueFilter) ([]OrderReconciliationIssue, int64, error) {
	m.lastFilter = filter
	return m.issues, m.total, m.listErr
}

func (m *mockOrderIssueStore) ActOnOrderIssue(_ context.Context, _, _ primitive.ObjectID, input OrderIssueActionInput) (OrderReconciliationIssue, error) {
	m.lastAction = input
	return m.action, m.actionErr
}

func TestOrderIssueScanAndListEndpoints(t *testing.T) {
	t.Run("scan", func(t *testing.T) {
		store := newMockOrderIssueStore()
		store.scanResult = OrderIssueScanResult{Scanned: 684, Open: 7, TotalVariance: 15600}
		response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/order-issues/scan?office_id="+testOfficeID,
			`{"start_date":"2026-07-01","end_date":"2026-07-31"}`, validTestClaims())
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"open":7`) {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	})

	t.Run("list with filters", func(t *testing.T) {
		store := newMockOrderIssueStore()
		store.total = 1
		store.issues = []OrderReconciliationIssue{{OrderNumber: "20260709-0052", Status: OrderIssueOpen}}
		path := "/api/earnings/order-issues?office_id=" + testOfficeID + "&start_date=2026-07-01&end_date=2026-07-31&status=open&issue_type=payment_total_mismatch&severity=high&search=0052&page=2&limit=20"
		response := performAPIRequest(t, store, http.MethodGet, path, "", validTestClaims())
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"total":1`) || store.lastFilter.Page != 2 || store.lastFilter.Search != "0052" {
			t.Fatalf("response=%d %s filter=%#v", response.Code, response.Body.String(), store.lastFilter)
		}
	})

	for _, test := range []struct {
		name, path string
	}{
		{"one date only", "/api/earnings/order-issues?office_id=" + testOfficeID + "&start_date=2026-07-01"},
		{"bad status", "/api/earnings/order-issues?office_id=" + testOfficeID + "&status=bad"},
		{"bad type", "/api/earnings/order-issues?office_id=" + testOfficeID + "&issue_type=bad"},
		{"bad severity", "/api/earnings/order-issues?office_id=" + testOfficeID + "&severity=bad"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performAPIRequest(t, newMockOrderIssueStore(), http.MethodGet, test.path, "", validTestClaims())
			if response.Code != http.StatusBadRequest {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}

	t.Run("repository errors and unavailable store", func(t *testing.T) {
		store := newMockOrderIssueStore()
		store.listErr = errStore
		response := performAPIRequest(t, store, http.MethodGet, "/api/earnings/order-issues?office_id="+testOfficeID, "", validTestClaims())
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("list response=%d", response.Code)
		}
		store.listErr, store.scanErr = nil, errStore
		response = performAPIRequest(t, store, http.MethodPost, "/api/earnings/order-issues/scan?office_id="+testOfficeID,
			`{"start_date":"2026-07-01","end_date":"2026-07-31"}`, validTestClaims())
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("scan response=%d", response.Code)
		}
		response = performAPIRequest(t, newMockStore(), http.MethodGet, "/api/earnings/order-issues?office_id="+testOfficeID, "", validTestClaims())
		if response.Code != http.StatusNotImplemented {
			t.Fatalf("unavailable response=%d", response.Code)
		}
	})
}

func TestOrderIssueActionEndpoint(t *testing.T) {
	issueID := primitive.NewObjectID().Hex()
	t.Run("aligns with payout permission and audit reason", func(t *testing.T) {
		store := newMockOrderIssueStore()
		store.action = OrderReconciliationIssue{Status: OrderIssueResolved}
		response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/order-issues/"+issueID+"/actions?office_id="+testOfficeID,
			`{"action":"align_payment_record","reason":"verified against gateway"}`, validTestClaims())
		if response.Code != http.StatusOK || store.lastAction.Action != OrderIssueActionAlign || store.lastAction.Reason == "" {
			t.Fatalf("response=%d %s action=%#v", response.Code, response.Body.String(), store.lastAction)
		}
	})

	for _, test := range []struct {
		name, id, body string
		want           int
	}{
		{"bad id", "bad", `{"action":"recheck"}`, http.StatusBadRequest},
		{"bad action", issueID, `{"action":"delete"}`, http.StatusBadRequest},
		{"missing reason", issueID, `{"action":"accept_variance"}`, http.StatusBadRequest},
		{"malformed body", issueID, `{`, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performAPIRequest(t, newMockOrderIssueStore(), http.MethodPost,
				"/api/earnings/order-issues/"+test.id+"/actions?office_id="+testOfficeID, test.body, validTestClaims())
			if response.Code != test.want {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}

	t.Run("maps domain errors", func(t *testing.T) {
		for err, want := range map[error]int{
			ErrOrderIssueNotFound: http.StatusNotFound, ErrOrderIssueAlreadyClosed: http.StatusConflict,
			ErrOrderIssueStillPresent: http.StatusConflict, ErrOrderIssueUnsupported: http.StatusConflict,
			errors.New("mongo"): http.StatusInternalServerError,
		} {
			store := newMockOrderIssueStore()
			store.actionErr = err
			response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/order-issues/"+issueID+"/actions?office_id="+testOfficeID,
				`{"action":"recheck"}`, validTestClaims())
			if response.Code != want {
				t.Fatalf("error=%v response=%d", err, response.Code)
			}
		}
	})

	t.Run("financial correction requires payout permission", func(t *testing.T) {
		claims := validTestClaims()
		payload := claims["payload"].(map[string]interface{})
		payload["is_admin"] = false
		payload["permissions"] = []string{"ledger.read", "ledger.rebuild"}
		response := performAPIRequest(t, newMockOrderIssueStore(), http.MethodPost, "/api/earnings/order-issues/"+issueID+"/actions?office_id="+testOfficeID,
			`{"action":"align_payment_record","reason":"verified"}`, claims)
		if response.Code != http.StatusForbidden {
			t.Fatalf("response=%d", response.Code)
		}
	})
}

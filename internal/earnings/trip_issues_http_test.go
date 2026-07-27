package earnings

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type mockTripIssueStore struct {
	*mockStore
	scanResult TripIssueScanResult
	scanErr    error
	issues     []TripReconciliationIssue
	total      int64
	listErr    error
	action     TripReconciliationIssue
	actionErr  error
	lastFilter TripIssueFilter
	lastAction TripIssueActionInput
}

func newMockTripIssueStore() *mockTripIssueStore {
	return &mockTripIssueStore{mockStore: newMockStore(), issues: []TripReconciliationIssue{}}
}

func (m *mockTripIssueStore) ScanTripIssues(context.Context, primitive.ObjectID, string, string) (TripIssueScanResult, error) {
	return m.scanResult, m.scanErr
}

func (m *mockTripIssueStore) ListTripIssues(_ context.Context, filter TripIssueFilter) ([]TripReconciliationIssue, int64, error) {
	m.lastFilter = filter
	return m.issues, m.total, m.listErr
}

func (m *mockTripIssueStore) ActOnTripIssue(_ context.Context, _, _ primitive.ObjectID, input TripIssueActionInput) (TripReconciliationIssue, error) {
	m.lastAction = input
	return m.action, m.actionErr
}

func TestTripIssueScanListAndActionEndpoints(t *testing.T) {
	store := newMockTripIssueStore()
	store.scanResult = TripIssueScanResult{Scanned: 100, Open: 4}
	response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/trip-issues/scan?office_id="+testOfficeID,
		`{"start_date":"2026-07-01","end_date":"2026-07-31"}`, validTestClaims())
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"open":4`) {
		t.Fatalf("scan response=%d %s", response.Code, response.Body.String())
	}

	store.total = 1
	store.issues = []TripReconciliationIssue{{TripNumber: "T-1", IssueType: TripIssueDeletedWorker}}
	path := "/api/earnings/trip-issues?office_id=" + testOfficeID + "&status=open&issue_type=deleted_worker_profile&severity=critical&search=Hiralal"
	response = performAPIRequest(t, store, http.MethodGet, path, "", validTestClaims())
	if response.Code != http.StatusOK || store.lastFilter.Search != "Hiralal" || store.lastFilter.IssueType != TripIssueDeletedWorker {
		t.Fatalf("list response=%d %s filter=%#v", response.Code, response.Body.String(), store.lastFilter)
	}

	issueID := primitive.NewObjectID().Hex()
	store.action = TripReconciliationIssue{Status: OrderIssueResolved}
	response = performAPIRequest(t, store, http.MethodPost, "/api/earnings/trip-issues/"+issueID+"/actions?office_id="+testOfficeID,
		`{"action":"rebuild_payable_snapshot","reason":"verified distance and rates"}`, validTestClaims())
	if response.Code != http.StatusOK || store.lastAction.Action != TripIssueActionRebuild || store.lastAction.Reason == "" {
		t.Fatalf("action response=%d %s action=%#v", response.Code, response.Body.String(), store.lastAction)
	}
}

func TestTripIssueEndpointValidationAndErrors(t *testing.T) {
	issueID := primitive.NewObjectID().Hex()
	for _, test := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"bad type", http.MethodGet, "/api/earnings/trip-issues?office_id=" + testOfficeID + "&issue_type=bad", "", http.StatusBadRequest},
		{"bad dates", http.MethodPost, "/api/earnings/trip-issues/scan?office_id=" + testOfficeID, `{"start_date":"bad","end_date":"2026-07-31"}`, http.StatusBadRequest},
		{"bad action", http.MethodPost, "/api/earnings/trip-issues/" + issueID + "/actions?office_id=" + testOfficeID, `{"action":"delete"}`, http.StatusBadRequest},
		{"missing reason", http.MethodPost, "/api/earnings/trip-issues/" + issueID + "/actions?office_id=" + testOfficeID, `{"action":"accept_variance"}`, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performAPIRequest(t, newMockTripIssueStore(), test.method, test.path, test.body, validTestClaims())
			if response.Code != test.want {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}

	store := newMockTripIssueStore()
	store.actionErr = ErrTripIssuePaid
	response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/trip-issues/"+issueID+"/actions?office_id="+testOfficeID,
		`{"action":"recheck"}`, validTestClaims())
	if response.Code != http.StatusConflict {
		t.Fatalf("paid response=%d", response.Code)
	}
	store.actionErr = errors.New("mongo")
	response = performAPIRequest(t, store, http.MethodPost, "/api/earnings/trip-issues/"+issueID+"/actions?office_id="+testOfficeID,
		`{"action":"recheck"}`, validTestClaims())
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("mongo response=%d", response.Code)
	}
}

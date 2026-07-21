package earnings

import (
	"net/http"
	"strings"
	"testing"
)

func TestReconciliationEndpoint(t *testing.T) {
	t.Run("returns office scoped result", func(t *testing.T) {
		store := newMockStore()
		store.reconciliation = ReconciliationResult{Ready: true, Matched: 3}
		response := performAPIRequest(t, store, http.MethodGet, "/api/earnings/reconciliation?office_id="+testOfficeID+"&start_date=2026-07-01&end_date=2026-07-31", "", validTestClaims())
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready":true`) {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	})

	t.Run("validates range", func(t *testing.T) {
		response := performAPIRequest(t, newMockStore(), http.MethodGet, "/api/earnings/reconciliation?office_id="+testOfficeID+"&start_date=bad&end_date=2026-07-31", "", validTestClaims())
		if response.Code != http.StatusBadRequest {
			t.Fatalf("response=%d", response.Code)
		}
	})

	t.Run("reports repository failure", func(t *testing.T) {
		store := newMockStore()
		store.reconciliationErr = errStore
		response := performAPIRequest(t, store, http.MethodGet, "/api/earnings/reconciliation?office_id="+testOfficeID+"&start_date=2026-07-01&end_date=2026-07-31", "", validTestClaims())
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("response=%d", response.Code)
		}
	})
}

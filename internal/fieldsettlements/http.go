package fieldsettlements

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/JeraldVictor/hom-swag-reporting/internal/earnings"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Store interface {
	ListSettlements(context.Context, earnings.SettlementFilter) ([]earnings.Settlement, int64, error)
}

type API struct{ store Store }

func NewAPI(store Store) *API { return &API{store: store} }

type request struct {
	OfficeID string `json:"office_id"`
	WorkerID string `json:"worker_id"`
	Page     int64  `json:"page"`
	Limit    int64  `json:"limit"`
}

func (a *API) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var input request
		decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			http.Error(w, "request must contain one JSON object", http.StatusBadRequest)
			return
		}
		if _, err := primitive.ObjectIDFromHex(input.OfficeID); err != nil {
			http.Error(w, "office_id must be a valid ObjectID", http.StatusBadRequest)
			return
		}
		if _, err := primitive.ObjectIDFromHex(input.WorkerID); err != nil {
			http.Error(w, "worker_id must be a valid ObjectID", http.StatusBadRequest)
			return
		}
		if input.Page < 1 {
			input.Page = 1
		}
		if input.Limit < 1 || input.Limit > 24 {
			input.Limit = 10
		}
		rows, total, err := a.store.ListSettlements(r.Context(), earnings.SettlementFilter{
			OfficeID: input.OfficeID, WorkerID: input.WorkerID, Page: input.Page, Limit: input.Limit,
		})
		if err != nil {
			http.Error(w, "failed to list settlements", http.StatusInternalServerError)
			return
		}
		if rows == nil {
			rows = make([]earnings.Settlement, 0)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"settlements": rows, "total": total, "page": input.Page, "limit": input.Limit})
	})
}

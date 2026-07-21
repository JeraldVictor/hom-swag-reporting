package fieldreports

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/earnings"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Store interface {
	LoadReportDetail(context.Context, primitive.ObjectID, primitive.ObjectID, string, string, string) (earnings.ReportDetail, error)
	WorkerBelongsToOffice(context.Context, string, primitive.ObjectID, primitive.ObjectID) (bool, error)
}

type API struct{ store Store }

func NewAPI(store Store) *API { return &API{store: store} }

type request struct {
	OfficeID  string `json:"office_id"`
	WorkerID  string `json:"worker_id"`
	Role      string `json:"role"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

func validDateRange(startDate, endDate string) bool {
	start, startErr := time.Parse("2006-01-02", startDate)
	end, endErr := time.Parse("2006-01-02", endDate)
	return startErr == nil && endErr == nil && !start.After(end)
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
		officeID, err := primitive.ObjectIDFromHex(strings.TrimSpace(input.OfficeID))
		if err != nil {
			http.Error(w, "office_id must be a valid ObjectID", http.StatusBadRequest)
			return
		}
		workerID, err := primitive.ObjectIDFromHex(strings.TrimSpace(input.WorkerID))
		if err != nil {
			http.Error(w, "worker_id must be a valid ObjectID", http.StatusBadRequest)
			return
		}
		if input.Role != "beautician" && input.Role != "rider" {
			http.Error(w, "role must be beautician or rider", http.StatusBadRequest)
			return
		}
		if !validDateRange(input.StartDate, input.EndDate) {
			http.Error(w, "start_date and end_date must be valid ordered dates", http.StatusBadRequest)
			return
		}
		belongs, err := a.store.WorkerBelongsToOffice(r.Context(), input.Role, workerID, officeID)
		if err != nil {
			http.Error(w, "failed to validate worker", http.StatusInternalServerError)
			return
		}
		if !belongs {
			http.Error(w, "worker does not belong to the selected office", http.StatusUnprocessableEntity)
			return
		}
		detail, err := a.store.LoadReportDetail(r.Context(), officeID, workerID, input.Role, input.StartDate, input.EndDate)
		if err != nil {
			http.Error(w, "failed to load report detail", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(detail)
	})
}

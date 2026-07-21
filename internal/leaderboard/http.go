package leaderboard

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type API struct {
	service *Service
	now     func() time.Time
}

func NewAPI(service *Service) *API { return &API{service: service, now: time.Now} }

type request struct {
	OfficeID string `json:"office_id"`
	Period   string `json:"period"`
	Role     string `json:"role"`
	Gender   string `json:"gender"`
	ViewerID string `json:"viewer_id"`
	Field    bool   `json:"field"`
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
		if input.Role != "beautician" && input.Role != "rider" {
			http.Error(w, "role must be beautician or rider", http.StatusBadRequest)
			return
		}
		if input.Gender == "" {
			input.Gender = "all"
		}
		if input.Gender != "all" && input.Gender != "male" && input.Gender != "female" && input.Gender != "other" {
			http.Error(w, "gender must be all, male, female, or other", http.StatusBadRequest)
			return
		}
		var viewerID primitive.ObjectID
		if input.Field {
			viewerID, err = primitive.ObjectIDFromHex(strings.TrimSpace(input.ViewerID))
			if err != nil {
				http.Error(w, "viewer_id must be a valid ObjectID for field requests", http.StatusBadRequest)
				return
			}
		}
		response, err := a.service.Get(r.Context(), Query{
			OfficeID: officeID, Period: input.Period, Role: input.Role, Gender: input.Gender,
			ViewerID: viewerID, Field: input.Field, Now: a.now(),
		})
		if errors.Is(err, ErrLeaderboardPermission) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
}

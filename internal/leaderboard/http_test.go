package leaderboard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func leaderboardRequest(api *API, method, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/leaderboard", strings.NewReader(body))
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestLeaderboardHTTPValidationAndResponses(t *testing.T) {
	officeID, viewerID := primitive.NewObjectID(), primitive.NewObjectID()
	baseBody := `{"office_id":"` + officeID.Hex() + `","period":"monthly","role":"rider","gender":"all"}`
	api := NewAPI(NewService(&mockLeaderboardStore{}))
	api.now = func() time.Time { return time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC) }

	for _, test := range []struct {
		name, method, body string
		want               int
	}{
		{name: "method", method: http.MethodGet, body: baseBody, want: 405},
		{name: "bad json", method: http.MethodPost, body: `{`, want: 400},
		{name: "unknown field", method: http.MethodPost, body: `{"bad":true}`, want: 400},
		{name: "trailing object", method: http.MethodPost, body: baseBody + `{}`, want: 400},
		{name: "invalid office", method: http.MethodPost, body: `{"office_id":"bad","period":"monthly","role":"rider"}`, want: 400},
		{name: "invalid role", method: http.MethodPost, body: `{"office_id":"` + officeID.Hex() + `","period":"monthly","role":"staff"}`, want: 400},
		{name: "invalid gender", method: http.MethodPost, body: `{"office_id":"` + officeID.Hex() + `","period":"monthly","role":"rider","gender":"unknown"}`, want: 400},
		{name: "invalid viewer", method: http.MethodPost, body: `{"office_id":"` + officeID.Hex() + `","period":"monthly","role":"rider","field":true,"viewer_id":"bad"}`, want: 400},
		{name: "invalid period", method: http.MethodPost, body: `{"office_id":"` + officeID.Hex() + `","period":"bad","role":"rider"}`, want: 500},
		{name: "custom missing dates", method: http.MethodPost, body: `{"office_id":"` + officeID.Hex() + `","period":"custom","role":"rider"}`, want: 400},
		{name: "custom reversed dates", method: http.MethodPost, body: `{"office_id":"` + officeID.Hex() + `","period":"custom","start_date":"2026-08-02","end_date":"2026-08-01","role":"rider"}`, want: 400},
		{name: "custom success", method: http.MethodPost, body: `{"office_id":"` + officeID.Hex() + `","period":"custom","start_date":"2026-08-01","end_date":"2026-08-02","role":"rider"}`, want: 200},
		{name: "success default gender", method: http.MethodPost, body: `{"office_id":"` + officeID.Hex() + `","period":"monthly","role":"rider"}`, want: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := leaderboardRequest(api, test.method, test.body)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}

	deniedStore := &mockLeaderboardStore{profiles: []Profile{{WorkerID: viewerID, CanViewLeaderboard: false}}}
	deniedAPI := NewAPI(NewService(deniedStore))
	response := leaderboardRequest(deniedAPI, http.MethodPost, `{"office_id":"`+officeID.Hex()+`","period":"monthly","role":"beautician","field":true,"viewer_id":"`+viewerID.Hex()+`"}`)
	if response.Code != 403 {
		t.Fatalf("permission status=%d body=%s", response.Code, response.Body.String())
	}

	failingAPI := NewAPI(NewService(&mockLeaderboardStore{riderErr: errors.New("store failure")}))
	response = leaderboardRequest(failingAPI, http.MethodPost, baseBody)
	if response.Code != 500 {
		t.Fatalf("failure status=%d body=%s", response.Code, response.Body.String())
	}
}

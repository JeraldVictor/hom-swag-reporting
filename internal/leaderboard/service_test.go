package leaderboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type mockLeaderboardStore struct {
	beauticianScores []SourceScore
	riderScores      []SourceScore
	profiles         []Profile
	prizes           PrizeSchedule
	beauticianErr    error
	riderErr         error
	profilesErr      error
	prizesErr        error
}

func (m *mockLeaderboardStore) BeauticianScores(context.Context, primitive.ObjectID, string, string) ([]SourceScore, error) {
	return m.beauticianScores, m.beauticianErr
}
func (m *mockLeaderboardStore) RiderScores(context.Context, primitive.ObjectID, string, string) ([]SourceScore, error) {
	return m.riderScores, m.riderErr
}
func (m *mockLeaderboardStore) Profiles(context.Context, string, []primitive.ObjectID, string) ([]Profile, error) {
	return m.profiles, m.profilesErr
}
func (m *mockLeaderboardStore) Prizes(context.Context, primitive.ObjectID) (PrizeSchedule, error) {
	return m.prizes, m.prizesErr
}

func TestPeriodBounds(t *testing.T) {
	now := time.Date(2026, time.February, 18, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		period, start, end string
		wantErr            bool
	}{
		{period: "weekly", start: "2026-02-12", end: "2026-02-19"},
		{period: "monthly", start: "2026-02-01", end: "2026-02-19"},
		{period: "yearly", start: "2026-01-01", end: "2026-02-19"},
		{period: "financial_year", start: "2025-04-01", end: "2026-02-19"},
		{period: "all_time", start: "0001-01-01", end: "2026-02-19"},
		{period: "invalid", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.period, func(t *testing.T) {
			start, end, err := PeriodBounds(test.period, now)
			if (err != nil) != test.wantErr || (!test.wantErr && (start != test.start || end != test.end)) {
				t.Fatalf("bounds=(%q,%q) err=%v", start, end, err)
			}
		})
	}
	start, _, err := PeriodBounds("financial_year", time.Date(2026, time.April, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || start != "2026-04-01" {
		t.Fatalf("april financial year start=%q err=%v", start, err)
	}
}

func TestQueryBoundsCustomRange(t *testing.T) {
	now := time.Date(2026, time.August, 8, 0, 0, 0, 0, time.UTC)
	start, end, err := QueryBounds("custom", "2026-07-01", "2026-07-31", now)
	if err != nil || start != "2026-07-01" || end != "2026-07-31" {
		t.Fatalf("bounds=(%q,%q) err=%v", start, end, err)
	}
	for _, dates := range [][2]string{
		{"", "2026-07-31"},
		{"2026-07-01", "bad"},
		{"2026-08-01", "2026-07-31"},
	} {
		if _, _, err := QueryBounds("custom", dates[0], dates[1], now); err == nil {
			t.Fatalf("expected custom range %q to fail", dates)
		}
	}
}

func TestServiceAdminBeauticianRanking(t *testing.T) {
	first, second, missing := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	store := &mockLeaderboardStore{
		beauticianScores: []SourceScore{
			{WorkerID: second, Count: 5, Amount: 1000},
			{WorkerID: first, Count: 6, Amount: 1000.129},
			{WorkerID: missing, Count: 99, Amount: 99999},
		},
		profiles: []Profile{
			{WorkerID: first, Name: "First", Photo: map[string]interface{}{"url": "first.jpg"}},
			{WorkerID: second, Name: "Second"},
		},
		prizes: PrizeSchedule{Beautician: []float64{500, 250}},
	}
	response, err := NewService(store).Get(context.Background(), Query{
		OfficeID: primitive.NewObjectID(), Period: "monthly", Role: "beautician", Gender: "female", Now: time.Now(),
	})
	if err != nil || len(response.Entries) != 2 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	if response.Entries[0].UserID != first.Hex() || response.Entries[0].Amount != 1000.13 || response.Entries[0].Prize != 500 || response.Entries[0].PhotoURL != "first.jpg" {
		t.Fatalf("first entry=%#v", response.Entries[0])
	}
	if response.IsRestricted || response.Gender != "female" || response.SelfEntry != nil {
		t.Fatalf("unexpected response metadata: %#v", response)
	}
}

func TestServiceFieldMaskingAndRiderRestriction(t *testing.T) {
	ids := make([]primitive.ObjectID, 7)
	beauticianStore := &mockLeaderboardStore{prizes: PrizeSchedule{Beautician: []float64{100}}}
	for index := range ids {
		ids[index] = primitive.NewObjectID()
		beauticianStore.beauticianScores = append(beauticianStore.beauticianScores, SourceScore{WorkerID: ids[index], Count: 7 - index, Amount: float64(700 - index*100)})
		beauticianStore.profiles = append(beauticianStore.profiles, Profile{WorkerID: ids[index], Name: "Person", CanViewLeaderboard: index == 5})
	}
	response, err := NewService(beauticianStore).Get(context.Background(), Query{
		OfficeID: primitive.NewObjectID(), Period: "monthly", Role: "beautician", Gender: "all", ViewerID: ids[5], Field: true, Now: time.Now(),
	})
	if err != nil || len(response.Entries) != 7 || response.SelfEntry == nil || response.SelfEntry.Rank != 6 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	if response.Entries[5].UserID == "masked" || response.Entries[6].UserID != "masked" || response.Entries[6].Name != "Masked User" || response.Entries[6].Photo != nil {
		t.Fatalf("masking failed: %#v %#v", response.Entries[5], response.Entries[6])
	}

	riderStore := &mockLeaderboardStore{prizes: PrizeSchedule{Rider: []float64{30, 20, 10}}}
	for index := range ids[:5] {
		riderStore.riderScores = append(riderStore.riderScores, SourceScore{WorkerID: ids[index], Count: 5 - index, Amount: float64(50 - index)})
		riderStore.profiles = append(riderStore.profiles, Profile{WorkerID: ids[index], Name: "Rider"})
	}
	riderResponse, err := NewService(riderStore).Get(context.Background(), Query{
		OfficeID: primitive.NewObjectID(), Period: "weekly", Role: "rider", ViewerID: ids[4], Field: true, Now: time.Now(),
	})
	if err != nil || len(riderResponse.Entries) != 3 || !riderResponse.IsRestricted || riderResponse.SelfEntry == nil || riderResponse.SelfEntry.Rank != 5 || riderResponse.SelfEntry.Score != 1 {
		t.Fatalf("rider response=%#v err=%v", riderResponse, err)
	}
}

func TestServiceErrorsAndLimits(t *testing.T) {
	errStore := errors.New("store failure")
	base := Query{OfficeID: primitive.NewObjectID(), Period: "monthly", Role: "beautician", Now: time.Now()}
	for _, test := range []struct {
		name  string
		store *mockLeaderboardStore
		query Query
		want  error
	}{
		{name: "invalid period", store: &mockLeaderboardStore{}, query: Query{Period: "bad"}},
		{name: "beautician scores", store: &mockLeaderboardStore{beauticianErr: errStore}, query: base, want: errStore},
		{name: "rider scores", store: &mockLeaderboardStore{riderErr: errStore}, query: Query{OfficeID: base.OfficeID, Period: "monthly", Role: "rider", Now: time.Now()}, want: errStore},
		{name: "profiles", store: &mockLeaderboardStore{profilesErr: errStore}, query: base, want: errStore},
		{name: "prizes", store: &mockLeaderboardStore{prizesErr: errStore}, query: base, want: errStore},
		{name: "viewer missing", store: &mockLeaderboardStore{}, query: Query{OfficeID: base.OfficeID, Period: "monthly", Role: "beautician", ViewerID: primitive.NewObjectID(), Field: true, Now: time.Now()}, want: ErrLeaderboardPermission},
		{name: "viewer denied", store: &mockLeaderboardStore{profiles: []Profile{{WorkerID: base.OfficeID}}}, query: Query{OfficeID: base.OfficeID, Period: "monthly", Role: "beautician", ViewerID: base.OfficeID, Field: true, Now: time.Now()}, want: ErrLeaderboardPermission},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewService(test.store).Get(context.Background(), test.query)
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
			if test.want == nil && err == nil {
				t.Fatal("expected an error")
			}
		})
	}

	large := &mockLeaderboardStore{}
	for index := 0; index < 1001; index++ {
		id := primitive.NewObjectID()
		large.riderScores = append(large.riderScores, SourceScore{WorkerID: id, Count: index, Amount: float64(index)})
		large.profiles = append(large.profiles, Profile{WorkerID: id, Name: "Rider"})
	}
	response, err := NewService(large).Get(context.Background(), Query{OfficeID: base.OfficeID, Period: "all_time", Role: "rider", Now: time.Now()})
	if err != nil || len(response.Entries) != 1000 {
		t.Fatalf("entries=%d err=%v", len(response.Entries), err)
	}
}

package leaderboard

import (
	"context"
	"errors"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var ErrLeaderboardPermission = errors.New("viewer does not have permission to view the leaderboard")

type Query struct {
	OfficeID  primitive.ObjectID
	Period    string
	StartDate string
	EndDate   string
	Role      string
	Gender    string
	ViewerID  primitive.ObjectID
	Field     bool
	Now       time.Time
}

type SourceScore struct {
	WorkerID primitive.ObjectID
	Count    int
	Amount   float64
}

type Profile struct {
	WorkerID           primitive.ObjectID     `bson:"_id"`
	Name               string                 `bson:"name"`
	Photo              map[string]interface{} `bson:"photo,omitempty"`
	Gender             string                 `bson:"gender,omitempty"`
	CanViewLeaderboard bool                   `bson:"can_view_leaderboard,omitempty"`
}

type PrizeSchedule struct {
	Beautician         []float64                    `bson:"beutician" json:"beutician"`
	Rider              []float64                    `bson:"rider" json:"rider"`
	BeauticianByGender BeauticianCommissionSettings `bson:"-" json:"beautician_by_gender,omitempty"`
	RiderByGender      BeauticianCommissionSettings `bson:"-" json:"rider_by_gender,omitempty"`
}

func (p PrizeSchedule) RiderPrizes(gender string) ([]float64, bool) {
	setting := p.RiderByGender.Female
	switch gender {
	case "male":
		setting = p.RiderByGender.Male
	case "other":
		setting = p.RiderByGender.Other
	}
	if setting.LeaderboardBonusEnabled == nil {
		return p.Rider, true
	}
	return setting.LeaderboardPrizes, *setting.LeaderboardBonusEnabled
}

type GenderCommissionSettings struct {
	LeaderboardBonusEnabled *bool     `bson:"leaderboard_bonus_enabled" json:"leaderboard_bonus_enabled,omitempty"`
	LeaderboardPrizes       []float64 `bson:"leaderboard_prizes" json:"leaderboard_prizes,omitempty"`
}

type BeauticianCommissionSettings struct {
	Female GenderCommissionSettings `bson:"female" json:"female"`
	Male   GenderCommissionSettings `bson:"male" json:"male"`
	Other  GenderCommissionSettings `bson:"other" json:"other"`
}

func (p PrizeSchedule) BeauticianPrizes(gender string) ([]float64, bool) {
	setting := p.BeauticianByGender.Female
	switch gender {
	case "male":
		setting = p.BeauticianByGender.Male
	case "other":
		setting = p.BeauticianByGender.Other
	}
	if setting.LeaderboardBonusEnabled == nil {
		return p.Beautician, true
	}
	return setting.LeaderboardPrizes, *setting.LeaderboardBonusEnabled
}

type Entry struct {
	Rank     int                    `json:"rank"`
	UserID   string                 `json:"user_id"`
	Name     string                 `json:"name"`
	Photo    map[string]interface{} `json:"photo,omitempty"`
	PhotoURL string                 `json:"photo_url,omitempty"`
	Count    int                    `json:"count"`
	Amount   float64                `json:"amount"`
	Score    int                    `json:"score,omitempty"`
	Prize    float64                `json:"prize,omitempty"`
	IsSelf   bool                   `json:"is_self,omitempty"`
}

type Response struct {
	Period       string        `json:"period"`
	StartDate    string        `json:"start_date"`
	EndDate      string        `json:"end_date"`
	Role         string        `json:"role"`
	Gender       string        `json:"gender,omitempty"`
	Entries      []Entry       `json:"entries"`
	SelfEntry    *Entry        `json:"self_entry,omitempty"`
	Prizes       PrizeSchedule `json:"prizes"`
	IsRestricted bool          `json:"is_restricted"`
}

type Store interface {
	BeauticianScores(context.Context, primitive.ObjectID, string, string) ([]SourceScore, error)
	RiderScores(context.Context, primitive.ObjectID, string, string) ([]SourceScore, error)
	Profiles(context.Context, string, []primitive.ObjectID, string) ([]Profile, error)
	Prizes(context.Context, primitive.ObjectID) (PrizeSchedule, error)
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) Get(ctx context.Context, query Query) (Response, error) {
	startDate, endDate, err := QueryBounds(query.Period, query.StartDate, query.EndDate, query.Now)
	if err != nil {
		return Response{}, err
	}
	var scores []SourceScore
	if query.Role == "beautician" {
		scores, err = s.store.BeauticianScores(ctx, query.OfficeID, startDate, endDate)
	} else {
		scores, err = s.store.RiderScores(ctx, query.OfficeID, startDate, endDate)
	}
	if err != nil {
		return Response{}, err
	}
	workerIDs := make([]primitive.ObjectID, 0, len(scores))
	for _, score := range scores {
		workerIDs = append(workerIDs, score.WorkerID)
	}
	if query.Field && query.Role == "beautician" && !query.ViewerID.IsZero() {
		workerIDs = append(workerIDs, query.ViewerID)
	}
	profiles, err := s.store.Profiles(ctx, query.Role, workerIDs, query.Gender)
	if err != nil {
		return Response{}, err
	}
	profileByID := make(map[primitive.ObjectID]Profile, len(profiles))
	for _, profile := range profiles {
		profileByID[profile.WorkerID] = profile
	}
	if query.Field && query.Role == "beautician" {
		viewer, found := profileByID[query.ViewerID]
		if !found || !viewer.CanViewLeaderboard {
			return Response{}, ErrLeaderboardPermission
		}
	}
	filtered := scores[:0]
	for _, score := range scores {
		if _, exists := profileByID[score.WorkerID]; exists {
			filtered = append(filtered, score)
		}
	}
	prizes, err := s.store.Prizes(ctx, query.OfficeID)
	if err != nil {
		return Response{}, err
	}
	var ranked []rankedSourceScore
	if query.Role == "beautician" {
		ranked = rankBeauticianSourceScoresByGender(filtered, profileByID, prizes)
	} else {
		ranked = rankRiderSourceScoresByGender(filtered, profileByID, prizes)
	}
	entries := make([]Entry, 0, len(ranked))
	for _, score := range ranked {
		profile := profileByID[score.WorkerID]
		entry := Entry{
			Rank: score.Rank, UserID: score.WorkerID.Hex(), Name: profile.Name, Photo: profile.Photo,
			Count: score.Count, Amount: roundTwo(score.Amount), Prize: score.Prize,
			IsSelf: score.WorkerID == query.ViewerID,
		}
		if query.Role == "rider" {
			entry.Score = score.Count
		}
		if url, ok := profile.Photo["url"].(string); ok {
			entry.PhotoURL = url
		}
		entries = append(entries, entry)
	}

	response := Response{
		Period: query.Period, StartDate: startDate, EndDate: endDate, Role: query.Role,
		Gender: query.Gender, Prizes: prizes, Entries: entries,
	}
	if !query.Field {
		if len(response.Entries) > 1000 {
			response.Entries = response.Entries[:1000]
		}
		return response, nil
	}
	response.IsRestricted = true
	for index := range entries {
		if entries[index].IsSelf {
			self := entries[index]
			response.SelfEntry = &self
			break
		}
	}
	if len(response.Entries) > 5 {
		response.Entries = response.Entries[:5]
	}
	return response, nil
}

type rankedSourceScore struct {
	SourceScore
	Prize float64
	Rank  int
}

func rankSourceScores(role string, scores []SourceScore, prizes PrizeSchedule) []rankedSourceScore {
	result := make([]rankedSourceScore, 0, len(scores))
	if role == "beautician" {
		businessScores := make([]BeauticianScore, len(scores))
		byID := make(map[primitive.ObjectID]SourceScore, len(scores))
		for index, score := range scores {
			businessScores[index] = BeauticianScore{WorkerID: score.WorkerID, Revenue: score.Amount, OrderCount: score.Count}
			byID[score.WorkerID] = score
		}
		for _, award := range RankBeauticians(businessScores, prizes.Beautician) {
			result = append(result, rankedSourceScore{SourceScore: byID[award.WorkerID], Prize: award.Bonus, Rank: award.Rank})
		}
		return result
	}
	businessScores := make([]RiderScore, len(scores))
	byID := make(map[primitive.ObjectID]SourceScore, len(scores))
	for index, score := range scores {
		businessScores[index] = RiderScore{WorkerID: score.WorkerID, TripCount: score.Count, TotalDistanceKM: score.Amount}
		byID[score.WorkerID] = score
	}
	for _, award := range RankRiders(businessScores, prizes.Rider) {
		result = append(result, rankedSourceScore{SourceScore: byID[award.WorkerID], Prize: award.Bonus, Rank: award.Rank})
	}
	return result
}

func rankBeauticianSourceScoresByGender(scores []SourceScore, profiles map[primitive.ObjectID]Profile, prizes PrizeSchedule) []rankedSourceScore {
	groups := map[string][]SourceScore{"female": {}, "male": {}, "other": {}}
	for _, score := range scores {
		gender := profiles[score.WorkerID].Gender
		if gender != "male" && gender != "other" {
			gender = "female"
		}
		groups[gender] = append(groups[gender], score)
	}
	result := make([]rankedSourceScore, 0, len(scores))
	for _, gender := range []string{"female", "male", "other"} {
		genderPrizes, enabled := prizes.BeauticianPrizes(gender)
		if !enabled {
			genderPrizes = nil
		}
		genderSchedule := PrizeSchedule{Beautician: genderPrizes}
		result = append(result, rankSourceScores("beautician", groups[gender], genderSchedule)...)
	}
	return result
}

func rankRiderSourceScoresByGender(scores []SourceScore, profiles map[primitive.ObjectID]Profile, prizes PrizeSchedule) []rankedSourceScore {
	groups := map[string][]SourceScore{"female": {}, "male": {}, "other": {}}
	for _, score := range scores {
		gender := profiles[score.WorkerID].Gender
		if gender != "male" && gender != "other" {
			gender = "female"
		}
		groups[gender] = append(groups[gender], score)
	}
	result := make([]rankedSourceScore, 0, len(scores))
	for _, gender := range []string{"female", "male", "other"} {
		genderPrizes, enabled := prizes.RiderPrizes(gender)
		if !enabled {
			genderPrizes = nil
		}
		result = append(result, rankSourceScores("rider", groups[gender], PrizeSchedule{Rider: genderPrizes})...)
	}
	return result
}

func roundTwo(value float64) float64 { return math.Round(value*100) / 100 }

func PeriodBounds(period string, now time.Time) (string, string, error) {
	location := time.FixedZone("IST", 5*60*60+30*60)
	today := now.In(location)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location)
	var start time.Time
	switch period {
	case "weekly":
		start = today.AddDate(0, 0, -7)
	case "monthly":
		start = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, location)
	case "yearly":
		start = time.Date(today.Year(), 1, 1, 0, 0, 0, 0, location)
	case "financial_year":
		year := today.Year()
		if today.Month() < time.April {
			year--
		}
		start = time.Date(year, time.April, 1, 0, 0, 0, 0, location)
	case "all_time":
		return "0001-01-01", today.Format("2006-01-02"), nil
	default:
		return "", "", errors.New("period must be weekly, monthly, yearly, financial_year, or all_time")
	}
	return start.Format("2006-01-02"), today.Format("2006-01-02"), nil
}

func QueryBounds(period, startDate, endDate string, now time.Time) (string, string, error) {
	if period != "custom" {
		return PeriodBounds(period, now)
	}
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return "", "", errors.New("start_date must use YYYY-MM-DD for a custom period")
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return "", "", errors.New("end_date must use YYYY-MM-DD for a custom period")
	}
	if start.After(end) {
		return "", "", errors.New("start_date must be on or before end_date")
	}
	return startDate, endDate, nil
}

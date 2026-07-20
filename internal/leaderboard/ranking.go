package leaderboard

import (
	"sort"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type BeauticianScore struct {
	WorkerID   primitive.ObjectID
	Revenue    float64
	OrderCount int
}

type RiderScore struct {
	WorkerID        primitive.ObjectID
	TripCount       int
	TotalDistanceKM float64
}

type Award struct {
	WorkerID primitive.ObjectID
	Rank     int
	Bonus    float64
}

// RankBeauticians ranks revenue first and order count second. ObjectID is the
// final key so equal business scores produce identical awards on every run.
func RankBeauticians(scores []BeauticianScore, prizes []float64) []Award {
	ranked := append([]BeauticianScore(nil), scores...)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Revenue != ranked[j].Revenue {
			return ranked[i].Revenue > ranked[j].Revenue
		}
		if ranked[i].OrderCount != ranked[j].OrderCount {
			return ranked[i].OrderCount > ranked[j].OrderCount
		}
		return ranked[i].WorkerID.Hex() < ranked[j].WorkerID.Hex()
	})
	return beauticianAwards(ranked, prizes)
}

func beauticianAwards(scores []BeauticianScore, prizes []float64) []Award {
	awards := make([]Award, len(scores))
	for index, score := range scores {
		bonus := 0.0
		if index < len(prizes) {
			bonus = prizes[index]
		}
		awards[index] = Award{WorkerID: score.WorkerID, Rank: index + 1, Bonus: bonus}
	}
	return awards
}

// RankRiders ranks trip count first and payable distance second, matching the
// rider commission report. ObjectID deterministically resolves exact ties.
func RankRiders(scores []RiderScore, prizes []float64) []Award {
	ranked := append([]RiderScore(nil), scores...)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].TripCount != ranked[j].TripCount {
			return ranked[i].TripCount > ranked[j].TripCount
		}
		if ranked[i].TotalDistanceKM != ranked[j].TotalDistanceKM {
			return ranked[i].TotalDistanceKM > ranked[j].TotalDistanceKM
		}
		return ranked[i].WorkerID.Hex() < ranked[j].WorkerID.Hex()
	})
	awards := make([]Award, len(ranked))
	for index, score := range ranked {
		bonus := 0.0
		if index < len(prizes) {
			bonus = prizes[index]
		}
		awards[index] = Award{WorkerID: score.WorkerID, Rank: index + 1, Bonus: bonus}
	}
	return awards
}

package earnings

import (
	"math"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func ptrFloat(value float64) *float64 { return &value }

func TestCanonicalTripCalculationUsesReportRules(t *testing.T) {
	trip := TripSource{
		IsTwoWay: true, AutoDistanceKM: 10, ExtraKM: 2, IsCommissionable: true,
		FareCalculation: TripFareCalculation{PetrolCostPerLiter: 110, StandardMileagePerLiter: 22},
		Snapshot:        &PayableSnapshot{CommissionRatePerKM: ptrFloat(1.5)},
	}
	got := canonicalTripCalculation(trip)
	if !got.Valid || got.Distance != 22 || got.Commission != 33 || got.Petrol != 110 {
		t.Fatalf("calculation=%#v", got)
	}

	trip.IsManualDistance = true
	trip.FareCalculation.TripDistanceKM = 8
	trip.CommissionAmount = 12.34
	got = canonicalTripCalculation(trip)
	if got.Distance != 8 || got.Commission != 12.34 || got.Petrol != 40 {
		t.Fatalf("manual calculation=%#v", got)
	}
}

func TestCanonicalTripCalculationRejectsInvalidInputs(t *testing.T) {
	trip := TripSource{AutoDistanceKM: math.NaN(), Snapshot: &PayableSnapshot{}}
	if canonicalTripCalculation(trip).Valid {
		t.Fatal("expected invalid distance")
	}
	trip.AutoDistanceKM = 10
	if got := canonicalTripCalculation(trip); got.Valid || got.Distance != 10 {
		t.Fatalf("expected invalid rates, got %#v", got)
	}
}

func TestAllowanceWorkerPrecedence(t *testing.T) {
	rider, beautician, driver := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	id, kind := allowanceWorker(TripSource{RiderID: &rider, BeauticianID: &beautician, DriverBeauticianID: &driver, IsSelfDrive: true})
	if id == nil || *id != driver || kind != "beautician" {
		t.Fatalf("driver precedence id=%v kind=%s", id, kind)
	}
	id, kind = allowanceWorker(TripSource{RiderID: &rider, BeauticianID: &beautician, IsSelfDrive: true})
	if id == nil || *id != beautician || kind != "beautician" {
		t.Fatalf("self-drive precedence id=%v kind=%s", id, kind)
	}
	id, kind = allowanceWorker(TripSource{RiderID: &rider})
	if id == nil || *id != rider || kind != "rider" {
		t.Fatalf("rider precedence id=%v kind=%s", id, kind)
	}
}

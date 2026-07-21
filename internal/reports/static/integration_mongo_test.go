package static

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type integrationSink struct {
	rows [][]interface{}
}

func (s *integrationSink) WriteRow(row []interface{}) error {
	s.rows = append(s.rows, row)
	return nil
}

func integrationMongoDB(t *testing.T) *mongo.Database {
	t.Helper()

	uri := os.Getenv("REPORTING_INTEGRATION_MONGODB_URI")
	if uri == "" {
		t.Skip("set REPORTING_INTEGRATION_MONGODB_URI to run Mongo aggregation integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect Mongo integration database: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatalf("ping Mongo integration database: %v", err)
	}

	dbName := "homswag_reporting_it_" + uuid.NewString()
	db := client.Database(dbName)
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = db.Drop(dropCtx)
		_ = client.Disconnect(dropCtx)
	})

	return db
}

func TestMongoIntegrationDailySalesAndCODPendingExecutors(t *testing.T) {
	db := integrationMongoDB(t)
	ctx := context.Background()

	officeID := primitive.NewObjectID()
	beauticianID := primitive.NewObjectID()
	otherOfficeID := primitive.NewObjectID()
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

	insertIntegrationDocs(t, ctx, db.Collection("beauticians"),
		bson.M{
			"_id":        beauticianID,
			"office_id":  officeID,
			"name":       "Meera",
			"emp_code":   "B-001",
			"pan_number": "ABCDE1234F",
		},
	)

	insertIntegrationDocs(t, ctx, db.Collection("orders"),
		bson.M{
			"_id":               primitive.NewObjectID(),
			"office_id":         officeID,
			"beautician_id":     beauticianID,
			"is_deleted":        false,
			"service_date":      now,
			"status":            "completed",
			"status_updated_at": now,
			"updated_at":        now,
			"invoice_number":    "INV-1",
			"order_number":      "ORD-1",
			"booking_info": bson.M{
				"date":         "2026-07-03",
				"surge_amount": 30,
			},
			"customer":                  bson.M{"full_name": "Asha"},
			"subtotal":                  1000,
			"convenience_fees":          50,
			"hygiene_fees":              20,
			"membership_charge":         100,
			"membership_discount_total": 100,
			"one_time_discount_amount":  40,
			"discount_total":            180,
			"total":                     1100,
			"payment": bson.M{
				"tip": 75,
				"history": bson.A{
					bson.M{"label": "UPI payment", "amount": 300},
					bson.M{"label": "Card payment", "amount": 400},
					bson.M{"label": "Bank transfer", "amount": 200},
					bson.M{"label": "Cash collected", "amount": 200},
					bson.M{"label": "Tip", "amount": 75},
				},
			},
			"cod_status":           "collected",
			"cod_collected_amount": 200,
		},
		bson.M{
			"_id":           primitive.NewObjectID(),
			"office_id":     officeID,
			"beautician_id": beauticianID,
			"status":        "completed",
			"updated_at":    now,
			"order_number":  "ORD-LEGACY",
			"booking_info":  bson.M{"date": "2026-07-04"},
			"customer":      bson.M{"full_name": "Legacy Customer"},
			"subtotal":      500,
			"total":         500,
			"payment": bson.M{
				"amount_paid": 500,
				"method":      "cod",
			},
			"cod_status":           "pending_return",
			"cod_collected_amount": 500,
		},
		bson.M{
			"_id":                 primitive.NewObjectID(),
			"office_id":           officeID,
			"beautician_id":       beauticianID,
			"is_deleted":          false,
			"service_date":        now,
			"status":              "arrived_and_cancelled",
			"status_updated_at":   now,
			"updated_at":          now,
			"order_number":        "ORD-CANCELLED",
			"booking_info":        bson.M{"date": "2026-07-03"},
			"customer":            bson.M{"full_name": "Cancelled Customer"},
			"subtotal":            900,
			"total":               900,
			"cancellation_charge": 150,
			"payment": bson.M{
				"actual_paid_amount": 150,
				"method":             "card",
			},
		},
		bson.M{
			"_id":           primitive.NewObjectID(),
			"office_id":     officeID,
			"beautician_id": beauticianID,
			"is_deleted":    true,
			"service_date":  now,
			"status":        "completed",
			"order_number":  "ORD-DELETED",
			"booking_info":  bson.M{"date": "2026-07-03"},
			"customer":      bson.M{"full_name": "Deleted"},
			"subtotal":      9999,
			"total":         9999,
		},
		bson.M{
			"_id":           primitive.NewObjectID(),
			"office_id":     otherOfficeID,
			"beautician_id": beauticianID,
			"is_deleted":    false,
			"service_date":  now,
			"status":        "completed",
			"order_number":  "ORD-OTHER-OFFICE",
			"booking_info":  bson.M{"date": "2026-07-03"},
			"customer":      bson.M{"full_name": "Other Office"},
			"subtotal":      7777,
			"total":         7777,
		},
	)

	dailySink := &integrationSink{}
	dailyReq := integrationRequest(officeID, "2026-07-01", "2026-07-31")
	dailyReq.Parameters["order_status"] = "all"
	if err := NewDailySalesExecutor(db).Run(ctx, dailyReq, dailySink); err != nil {
		t.Fatalf("daily sales run: %v", err)
	}
	if got, want := len(dailySink.rows), 5; got != want {
		t.Fatalf("daily rows = %d, want header + 3 data rows + total", got)
	}
	assertIntegrationTotal(t, dailySink.rows, "Total Services cost", 2400)
	assertIntegrationTotal(t, dailySink.rows, "Cancellation Charges", 150)
	assertIntegrationTotal(t, dailySink.rows, "Net Receivable including GST", 1675)
	assertIntegrationTotal(t, dailySink.rows, "Total Received", 1750)

	codSink := &integrationSink{}
	if err := NewCODPendingExecutor(db).Run(ctx, integrationRequest(officeID, "2026-07-01", "2026-07-31"), codSink); err != nil {
		t.Fatalf("cod pending run: %v", err)
	}
	if got, want := len(codSink.rows), 3; got != want {
		t.Fatalf("cod rows = %d, want header + 2 data rows", got)
	}
	assertIntegrationColumnSum(t, codSink.rows, "COD Collected Amount", 700)
}

func TestMongoIntegrationCommissionAndPetrolExecutors(t *testing.T) {
	db := integrationMongoDB(t)
	ctx := context.Background()

	officeID := primitive.NewObjectID()
	beauticianID := primitive.NewObjectID()
	riderID := primitive.NewObjectID()
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

	insertIntegrationDocs(t, ctx, db.Collection("offices"),
		bson.M{
			"_id":                        officeID,
			"petrol_cost_per_liter":      120,
			"standard_mileage_per_liter": 40,
			"monthly_target2_bonus":      50,
			"leaderboard_prizes":         bson.M{"rider": bson.A{25}, "beutician": bson.A{35}},
		},
	)
	insertIntegrationDocs(t, ctx, db.Collection("beauticians"),
		bson.M{
			"_id":             beauticianID,
			"office_id":       officeID,
			"name":            "Meera",
			"emp_code":        "B-001",
			"monthly_target1": 1000,
			"monthly_target2": 2000,
		},
	)
	insertIntegrationDocs(t, ctx, db.Collection("riders"),
		bson.M{
			"_id":       riderID,
			"office_id": officeID,
			"name":      "Ravi",
			"emp_code":  "R-001",
		},
	)
	insertIntegrationDocs(t, ctx, db.Collection("leaderboards"),
		bson.M{
			"office_id":     officeID,
			"beautician_id": beauticianID,
			"date":          now,
			"revenue":       1500,
			"order_count":   1,
		},
	)
	insertIntegrationDocs(t, ctx, db.Collection("orders"),
		bson.M{
			"_id":           primitive.NewObjectID(),
			"office_id":     officeID,
			"beautician_id": beauticianID,
			"is_deleted":    false,
			"service_date":  now,
			"status":        "completed",
			"booking_info":  bson.M{"date": "2026-07-03"},
			"customer":      bson.M{"full_name": "Asha"},
			"order_number":  "ORD-1",
			"order_cost":    1500,
			"commission_details": bson.M{
				"special_commission":       75,
				"general_commission":       120,
				"upgrade_addon_commission": 45,
			},
		},
		bson.M{
			"_id":           primitive.NewObjectID(),
			"office_id":     officeID,
			"beautician_id": beauticianID,
			"is_deleted":    false,
			"service_date":  now,
			"status":        "cancelled_and_refunded",
			"booking_info":  bson.M{"date": "2026-07-03"},
			"customer":      bson.M{"full_name": "Refunded"},
			"order_number":  "ORD-REFUND",
			"payment":       bson.M{"partial_refund_amount": 300},
		},
	)
	insertIntegrationDocs(t, ctx, db.Collection("trips"),
		bson.M{
			"_id":                      primitive.NewObjectID(),
			"office_id":                officeID,
			"rider_id":                 riderID,
			"date":                     "2026-07-03",
			"is_deleted":               false,
			"kanban_state":             "trip_completed",
			"is_two_way":               false,
			"is_commission_applicable": true,
			"commission_amount":        10,
			"fare_calculation": bson.M{
				"trip_distance_km": 18,
				"calculated_fare":  90,
			},
		},
		bson.M{
			"_id":                      primitive.NewObjectID(),
			"office_id":                officeID,
			"beautician_id":            beauticianID,
			"is_self_drive":            true,
			"date":                     "2026-07-04",
			"status":                   "completed",
			"auto_distance_km":         12,
			"extra_km":                 3,
			"is_two_way":               true,
			"is_commission_applicable": true,
		},
	)

	beauticianSink := &integrationSink{}
	if err := NewBeauticianCommissionExecutor(db).Run(ctx, integrationRequest(officeID, "2026-07-01", "2026-07-31"), beauticianSink); err != nil {
		t.Fatalf("beautician commission run: %v", err)
	}
	if got, want := len(beauticianSink.rows), 2; got != want {
		t.Fatalf("beautician rows = %d, want header + 1 row", got)
	}
	assertIntegrationCell(t, beauticianSink.rows, 1, "Order Count", 1)
	assertIntegrationCell(t, beauticianSink.rows, 1, "Total Revenue", "1500.00")
	assertIntegrationCell(t, beauticianSink.rows, 1, "Refunded", "300.00")
	assertIntegrationCell(t, beauticianSink.rows, 1, "Total Commission", "275.00")

	riderSink := &integrationSink{}
	if err := NewRiderCommissionExecutor(db).Run(ctx, integrationRequest(officeID, "2026-07-01", "2026-07-31"), riderSink); err != nil {
		t.Fatalf("rider commission run: %v", err)
	}
	if got, want := len(riderSink.rows), 3; got != want {
		t.Fatalf("rider commission rows = %d, want header + rider + self-drive", got)
	}
	assertIntegrationColumnSum(t, riderSink.rows, "Trip Count", 2)
	assertIntegrationColumnSum(t, riderSink.rows, "Total Distance KM", 45)
	assertIntegrationColumnSum(t, riderSink.rows, "Petrol Payable", 135)

	petrolSink := &integrationSink{}
	if err := NewPetrolWeeklyExecutor(db).Run(ctx, integrationRequest(officeID, "2026-07-01", "2026-07-31"), petrolSink); err != nil {
		t.Fatalf("petrol weekly run: %v", err)
	}
	if got, want := len(petrolSink.rows), 3; got != want {
		t.Fatalf("petrol rows = %d, want header + rider + self-drive", got)
	}
	assertIntegrationColumnSum(t, petrolSink.rows, "Total Distance (KM)", 45)
	assertIntegrationColumnSum(t, petrolSink.rows, "Payable Amount", 135)
}

func insertIntegrationDocs(t *testing.T, ctx context.Context, collection *mongo.Collection, docs ...interface{}) {
	t.Helper()
	if _, err := collection.InsertMany(ctx, docs); err != nil {
		t.Fatalf("insert %s docs: %v", collection.Name(), err)
	}
}

func integrationRequest(officeID primitive.ObjectID, startDate string, endDate string) reports.Request {
	return reports.Request{
		Parameters: map[string]interface{}{
			"office_id":  officeID.Hex(),
			"start_date": startDate,
			"end_date":   endDate,
		},
	}
}

func assertIntegrationTotal(t *testing.T, rows [][]interface{}, column string, expected float64) {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("rows are empty")
	}
	totalRow := rows[len(rows)-1]
	assertIntegrationFloatCell(t, rows[0], totalRow, column, expected)
}

func assertIntegrationColumnSum(t *testing.T, rows [][]interface{}, column string, expected float64) {
	t.Helper()
	if len(rows) < 2 {
		t.Fatalf("expected data rows for %s, got %#v", column, rows)
	}
	index := integrationColumnIndex(t, rows[0], column)
	total := 0.0
	for _, row := range rows[1:] {
		total += integrationFloatValue(t, row[index])
	}
	if total != expected {
		t.Fatalf("%s sum = %v, want %v", column, total, expected)
	}
}

func assertIntegrationCell(t *testing.T, rows [][]interface{}, rowIndex int, column string, expected interface{}) {
	t.Helper()
	if rowIndex >= len(rows) {
		t.Fatalf("row index %d out of range for %#v", rowIndex, rows)
	}
	index := integrationColumnIndex(t, rows[0], column)
	if got := rows[rowIndex][index]; got != expected {
		t.Fatalf("%s row %d = %#v, want %#v", column, rowIndex, got, expected)
	}
}

func assertIntegrationFloatCell(t *testing.T, header []interface{}, row []interface{}, column string, expected float64) {
	t.Helper()
	index := integrationColumnIndex(t, header, column)
	got := integrationFloatValue(t, row[index])
	if got != expected {
		t.Fatalf("%s = %v, want %v", column, got, expected)
	}
}

func integrationColumnIndex(t *testing.T, header []interface{}, column string) int {
	t.Helper()
	for index, value := range header {
		if fmt.Sprint(value) == column {
			return index
		}
	}
	t.Fatalf("column %s not found in header %#v", column, header)
	return -1
}

func integrationFloatValue(t *testing.T, value interface{}) float64 {
	t.Helper()
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	case string:
		var parsed float64
		if _, err := fmt.Sscanf(typed, "%f", &parsed); err != nil {
			t.Fatalf("parse float value %q: %v", typed, err)
		}
		return parsed
	default:
		t.Fatalf("unsupported numeric value %#v", value)
	}
	return 0
}

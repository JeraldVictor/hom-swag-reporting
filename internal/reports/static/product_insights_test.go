package static

import (
	"context"
	"reflect"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestProductInsightsValidateAndDefaults(t *testing.T) {
	officeID := primitive.NewObjectID()
	filters, err := parseProductInsightsFilters(map[string]interface{}{
		"start_date": "2026-07-01",
		"end_date":   "2026-07-31",
		"office_id":  officeID.Hex(),
	})
	if err != nil {
		t.Fatalf("parse filters: %v", err)
	}
	if filters.OrderStatus != "completed" {
		t.Fatalf("order status = %q, want completed", filters.OrderStatus)
	}
	if filters.ProductType != productInsightsAll {
		t.Fatalf("product type = %q, want all", filters.ProductType)
	}

	executor := NewProductInsightsExecutor(nil)
	if err := executor.Validate(context.Background(), reports.Request{Parameters: map[string]interface{}{
		"start_date": "2026-07-01",
		"end_date":   "2026-07-31",
		"office_id":  officeID.Hex(),
	}}); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestProductInsightsRejectsInvalidCatalogFilters(t *testing.T) {
	base := map[string]interface{}{
		"start_date": "2026-07-01",
		"end_date":   "2026-07-31",
		"office_id":  primitive.NewObjectID().Hex(),
	}
	tests := []struct {
		name  string
		key   string
		value interface{}
	}{
		{name: "bad menu", key: "main_menu_ids", value: []interface{}{"bad"}},
		{name: "bad category type", key: "category_ids", value: []interface{}{42}},
		{name: "bad product type", key: "product_type", value: "retail"},
		{name: "bad status", key: "order_status", value: "unknown"},
		{name: "bad search", key: "product_search", value: 42},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parameters := make(map[string]interface{}, len(base)+1)
			for key, value := range base {
				parameters[key] = value
			}
			parameters[test.key] = test.value
			if _, err := parseProductInsightsFilters(parameters); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestReportObjectIDListParamDeduplicatesValues(t *testing.T) {
	idA := primitive.NewObjectID()
	idB := primitive.NewObjectID()
	got, err := reportObjectIDListParam(map[string]interface{}{
		"category_ids": []interface{}{idA.Hex(), idB.Hex(), idA.Hex(), ""},
	}, "category_ids")
	if err != nil {
		t.Fatalf("parse IDs: %v", err)
	}
	if want := []primitive.ObjectID{idA, idB}; !reflect.DeepEqual(got, want) {
		t.Fatalf("IDs = %#v, want %#v", got, want)
	}
}

func TestProductInsightsPipelineRanksByQuantity(t *testing.T) {
	filters, err := parseProductInsightsFilters(map[string]interface{}{
		"start_date":       "2026-07-01",
		"end_date":         "2026-07-31",
		"office_id":        primitive.NewObjectID().Hex(),
		"main_menu_ids":    []interface{}{primitive.NewObjectID().Hex()},
		"category_ids":     []interface{}{primitive.NewObjectID().Hex()},
		"sub_category_ids": []interface{}{primitive.NewObjectID().Hex()},
		"product_type":     "service",
		"product_search":   "facial",
	})
	if err != nil {
		t.Fatalf("parse filters: %v", err)
	}

	pipeline := productInsightsPipeline(filters, 25)
	lastStage := pipeline[len(pipeline)-1]
	if got := lastStage.Map()["$limit"]; got != 25 {
		t.Fatalf("last stage limit = %#v, want 25", got)
	}

	foundSort := false
	for _, stage := range pipeline {
		sortValue, ok := stage.Map()["$sort"]
		if !ok {
			continue
		}
		sortDocument, ok := sortValue.(bson.D)
		if !ok || len(sortDocument) == 0 {
			continue
		}
		if sortDocument[0].Key == "total_quantity" && sortDocument[0].Value == -1 {
			foundSort = true
			break
		}
	}
	if !foundSort {
		t.Fatal("quantity-descending sort stage not found")
	}
}

func TestProductInsightsRowCalculatesAverageRevenue(t *testing.T) {
	row := productInsightsRow{
		ProductID:       primitive.NewObjectID(),
		ProductName:     "Facial",
		ProductType:     "service",
		MainMenuName:    "Women",
		CategoryName:    "Face Care",
		SubCategoryName: "Facials",
		TotalQuantity:   4,
		OrderCount:      3,
		GrossSales:      1500,
	}
	values := row.values(2)
	if values[0] != int64(2) || values[7] != int64(4) || values[8] != int64(3) {
		t.Fatalf("unexpected rank/count values: %#v", values)
	}
	if values[10] != float64(375) {
		t.Fatalf("average revenue = %#v, want 375", values[10])
	}
}

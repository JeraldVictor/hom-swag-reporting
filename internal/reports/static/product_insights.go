package static

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

const productInsightsAll = "all"

type ProductInsightsExecutor struct {
	db *mongo.Database
}

type productInsightsFilters struct {
	StartDate      time.Time
	EndDate        time.Time
	OfficeID       primitive.ObjectID
	MainMenuIDs    []primitive.ObjectID
	CategoryIDs    []primitive.ObjectID
	SubCategoryIDs []primitive.ObjectID
	OrderStatus    string
	ProductType    string
	ProductSearch  string
}

type productInsightsRow struct {
	ProductID       primitive.ObjectID `bson:"product_id"`
	ProductName     string             `bson:"product_name"`
	ProductType     string             `bson:"product_type"`
	MainMenuName    string             `bson:"main_menu_name"`
	CategoryName    string             `bson:"category_name"`
	SubCategoryName string             `bson:"sub_category_name"`
	TotalQuantity   int64              `bson:"total_quantity"`
	OrderCount      int64              `bson:"order_count"`
	GrossSales      float64            `bson:"gross_sales"`
}

func NewProductInsightsExecutor(db *mongo.Database) *ProductInsightsExecutor {
	return &ProductInsightsExecutor{db: db}
}

func (e *ProductInsightsExecutor) Key() string {
	return "product_insights"
}

func (e *ProductInsightsExecutor) Version() int {
	return 1
}

func (e *ProductInsightsExecutor) Columns() []reports.Column {
	return withColumnDescriptions(productInsightsColumns)
}

func (e *ProductInsightsExecutor) Definition() reports.DefinitionMetadata {
	return reports.DefinitionMetadata{
		Filters: []reports.Filter{
			{Key: "start_date", Label: "Order Start Date", Type: "date", Required: true},
			{Key: "end_date", Label: "Order End Date", Type: "date", Required: true},
			{
				Key: "order_status", Label: "Order Status", Type: "enum",
				Options: []reports.FilterOption{
					{Label: "All", Value: productInsightsAll},
					{Label: "Pending", Value: "pending"},
					{Label: "Assigned Draft", Value: "assigned_draft"},
					{Label: "Confirmed", Value: "confirmed"},
					{Label: "Ongoing", Value: "ongoing"},
					{Label: "Reached Customer Place", Value: "reached_customer_place"},
					{Label: "Started", Value: "started"},
					{Label: "Completed", Value: "completed"},
					{Label: "Cancelled", Value: "cancelled"},
					{Label: "Cancellation Requested", Value: "cancel_requested"},
					{Label: "Cancelled and Refunded", Value: "cancelled_and_refunded"},
					{Label: "Re-Assign Required", Value: "re_assign_required"},
					{Label: "Arrived and Cancelled", Value: "arrived_and_cancelled"},
				},
			},
			{Key: "main_menu_ids", Label: "Main Menus", Type: "object_id"},
			{Key: "category_ids", Label: "Categories", Type: "object_id"},
			{Key: "sub_category_ids", Label: "Subcategories", Type: "object_id"},
			{
				Key: "product_type", Label: "Product Type", Type: "enum",
				Options: []reports.FilterOption{
					{Label: "All", Value: productInsightsAll},
					{Label: "Service", Value: "service"},
					{Label: "Package", Value: "package"},
				},
			},
			{Key: "product_search", Label: "Product Name", Type: "string"},
		},
		AllowedFormats: []string{"CSV", "XLSX"},
		DefaultFormat:  "XLSX",
	}
}

func (e *ProductInsightsExecutor) Validate(_ context.Context, req reports.Request) error {
	_, err := parseProductInsightsFilters(req.Parameters)
	return err
}

func (e *ProductInsightsExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
	filters, err := parseProductInsightsFilters(req.Parameters)
	if err != nil {
		return err
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, productInsightsPipeline(filters, req.Limit))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	header := make([]interface{}, len(productInsightsColumns))
	for index, column := range productInsightsColumns {
		header[index] = column.Label
	}
	if err := sink.WriteRow(header); err != nil {
		return err
	}

	rank := int64(0)
	for cursor.Next(ctx) {
		var row productInsightsRow
		if err := cursor.Decode(&row); err != nil {
			return err
		}
		rank++
		if err := sink.WriteRow(row.values(rank)); err != nil {
			return err
		}
	}

	return cursor.Err()
}

func (r productInsightsRow) values(rank int64) []interface{} {
	averageUnitRevenue := 0.0
	if r.TotalQuantity > 0 {
		averageUnitRevenue = money2(r.GrossSales / float64(r.TotalQuantity))
	}

	return []interface{}{
		rank,
		r.ProductID.Hex(),
		r.ProductName,
		r.ProductType,
		r.MainMenuName,
		r.CategoryName,
		r.SubCategoryName,
		r.TotalQuantity,
		r.OrderCount,
		money2(r.GrossSales),
		averageUnitRevenue,
	}
}

func parseProductInsightsFilters(parameters map[string]interface{}) (productInsightsFilters, error) {
	if err := validateReportDateRange(parameters, parseDailySalesDate); err != nil {
		return productInsightsFilters{}, err
	}

	startDateText, _ := parameters["start_date"].(string)
	endDateText, _ := parameters["end_date"].(string)
	startDate, _ := parseDailySalesDate(startDateText, false)
	endDate, _ := parseDailySalesDate(endDateText, true)

	officeIDText, err := reportStringParam(parameters, "office_id")
	if err != nil {
		return productInsightsFilters{}, err
	}
	officeID, err := primitive.ObjectIDFromHex(officeIDText)
	if err != nil {
		return productInsightsFilters{}, fmt.Errorf("invalid office_id: %w", err)
	}

	filters := productInsightsFilters{
		StartDate:   startDate,
		EndDate:     endDate,
		OfficeID:    officeID,
		OrderStatus: "completed",
		ProductType: productInsightsAll,
	}

	if raw, ok := parameters["order_status"]; ok {
		status, ok := raw.(string)
		if !ok {
			return productInsightsFilters{}, fmt.Errorf("order_status must be a string")
		}
		status = strings.TrimSpace(status)
		if status == "" {
			return productInsightsFilters{}, fmt.Errorf("order_status must not be empty")
		}
		if status != productInsightsAll {
			if _, allowed := customerBookingStatuses[status]; !allowed {
				return productInsightsFilters{}, fmt.Errorf("invalid order_status: %s", status)
			}
		}
		filters.OrderStatus = status
	}

	filters.MainMenuIDs, err = reportObjectIDListParam(parameters, "main_menu_ids")
	if err != nil {
		return productInsightsFilters{}, err
	}
	filters.CategoryIDs, err = reportObjectIDListParam(parameters, "category_ids")
	if err != nil {
		return productInsightsFilters{}, err
	}
	filters.SubCategoryIDs, err = reportObjectIDListParam(parameters, "sub_category_ids")
	if err != nil {
		return productInsightsFilters{}, err
	}

	if raw, ok := parameters["product_type"]; ok {
		productType, ok := raw.(string)
		if !ok {
			return productInsightsFilters{}, fmt.Errorf("product_type must be a string")
		}
		productType = strings.TrimSpace(productType)
		if productType != productInsightsAll && productType != "service" && productType != "package" {
			return productInsightsFilters{}, fmt.Errorf("invalid product_type: %s", productType)
		}
		filters.ProductType = productType
	}

	if raw, ok := parameters["product_search"]; ok {
		search, ok := raw.(string)
		if !ok {
			return productInsightsFilters{}, fmt.Errorf("product_search must be a string")
		}
		filters.ProductSearch = strings.TrimSpace(search)
	}

	return filters, nil
}

func reportObjectIDListParam(parameters map[string]interface{}, key string) ([]primitive.ObjectID, error) {
	raw, exists := parameters[key]
	if !exists || raw == nil {
		return nil, nil
	}

	values := make([]string, 0)
	switch value := raw.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			values = append(values, value)
		}
	case []string:
		values = append(values, value...)
	case []interface{}:
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must contain only strings", key)
			}
			values = append(values, text)
		}
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}

	ids := make([]primitive.ObjectID, 0, len(values))
	seen := make(map[primitive.ObjectID]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		id, err := primitive.ObjectIDFromHex(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid %s value: %w", key, err)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func productInsightsPipeline(filters productInsightsFilters, limit int) mongo.Pipeline {
	match := orderReportBaseMatch(
		filters.StartDate,
		filters.EndDate,
		filters.StartDate.Format("2006-01-02"),
		filters.EndDate.Format("2006-01-02"),
	)
	match["office_id"] = filters.OfficeID
	if filters.OrderStatus != productInsightsAll {
		match["status"] = filters.OrderStatus
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$unwind", Value: "$products"}},
		{{Key: "$match", Value: bson.M{"products.product_id": bson.M{"$ne": nil}}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "products",
			"localField":   "products.product_id",
			"foreignField": "_id",
			"as":           "catalog_product",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$catalog_product", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "subcategories",
			"localField":   "catalog_product.sub_category_id",
			"foreignField": "_id",
			"as":           "sub_category",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$sub_category", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "categories",
			"localField":   "sub_category.category_id",
			"foreignField": "_id",
			"as":           "category",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$category", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "mainmenus",
			"localField":   "category.menu_id",
			"foreignField": "_id",
			"as":           "main_menu",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$main_menu", "preserveNullAndEmptyArrays": true}}},
	}

	catalogMatch := bson.M{}
	if len(filters.MainMenuIDs) > 0 {
		catalogMatch["category.menu_id"] = bson.M{"$in": filters.MainMenuIDs}
	}
	if len(filters.CategoryIDs) > 0 {
		catalogMatch["sub_category.category_id"] = bson.M{"$in": filters.CategoryIDs}
	}
	if len(filters.SubCategoryIDs) > 0 {
		catalogMatch["catalog_product.sub_category_id"] = bson.M{"$in": filters.SubCategoryIDs}
	}
	if filters.ProductType != productInsightsAll {
		catalogMatch["catalog_product.type"] = filters.ProductType
	}
	if filters.ProductSearch != "" {
		catalogMatch["$or"] = bson.A{
			bson.M{"catalog_product.title": primitive.Regex{Pattern: regexp.QuoteMeta(filters.ProductSearch), Options: "i"}},
			bson.M{"products.title": primitive.Regex{Pattern: regexp.QuoteMeta(filters.ProductSearch), Options: "i"}},
		}
	}
	if len(catalogMatch) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: catalogMatch}})
	}

	pipeline = append(pipeline,
		bson.D{{Key: "$group", Value: bson.M{
			"_id":               "$products.product_id",
			"product_name":      bson.M{"$first": bson.M{"$ifNull": bson.A{"$catalog_product.title", "$products.title"}}},
			"product_type":      bson.M{"$first": bson.M{"$ifNull": bson.A{"$catalog_product.type", ""}}},
			"main_menu_name":    bson.M{"$first": bson.M{"$ifNull": bson.A{"$main_menu.title", ""}}},
			"category_name":     bson.M{"$first": bson.M{"$ifNull": bson.A{"$category.title", ""}}},
			"sub_category_name": bson.M{"$first": bson.M{"$ifNull": bson.A{"$sub_category.title", ""}}},
			"total_quantity":    bson.M{"$sum": bson.M{"$ifNull": bson.A{"$products.quantity", 1}}},
			"order_ids":         bson.M{"$addToSet": "$_id"},
			"gross_sales":       bson.M{"$sum": bson.M{"$ifNull": bson.A{"$products.total", 0}}},
		}}},
		bson.D{{Key: "$project", Value: bson.M{
			"_id":               0,
			"product_id":        "$_id",
			"product_name":      1,
			"product_type":      1,
			"main_menu_name":    1,
			"category_name":     1,
			"sub_category_name": 1,
			"total_quantity":    1,
			"order_count":       bson.M{"$size": "$order_ids"},
			"gross_sales":       1,
		}}},
		bson.D{{Key: "$sort", Value: bson.D{
			{Key: "total_quantity", Value: -1},
			{Key: "order_count", Value: -1},
			{Key: "gross_sales", Value: -1},
			{Key: "product_name", Value: 1},
		}}},
	)

	if limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: limit}})
	}
	return pipeline
}

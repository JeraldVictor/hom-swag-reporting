package static

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

const (
	customerStatusAll      = "all"
	customerStatusActive   = "active"
	customerStatusInactive = "inactive"
)

var customerBookingStatuses = map[string]struct{}{
	"pending":                {},
	"assigned_draft":         {},
	"confirmed":              {},
	"ongoing":                {},
	"reached_customer_place": {},
	"started":                {},
	"completed":              {},
	"cancelled":              {},
	"cancel_requested":       {},
	"cancelled_and_refunded": {},
	"re_assign_required":     {},
	"arrived_and_cancelled":  {},
}

type CustomerBookingExecutor struct {
	db *mongo.Database
}

type customerBookingFilters struct {
	StartDate      time.Time
	EndDate        time.Time
	OfficeID       primitive.ObjectID
	ZoneIDs        []primitive.ObjectID
	OrderStatus    string
	CustomerStatus string
	CustomerSearch string
}

type customerBookingRow struct {
	CustomerID       primitive.ObjectID `bson:"customer_id"`
	CustomerName     string             `bson:"customer_name"`
	Phone            string             `bson:"phone"`
	AlternativePhone string             `bson:"alternative_phone"`
	Email            string             `bson:"email"`
	Gender           string             `bson:"gender"`
	IsActive         bool               `bson:"is_active"`
	RegisteredAt     time.Time          `bson:"registered_at"`
	LastBookingDate  time.Time          `bson:"last_booking_date"`
	BookingCount     int64              `bson:"booking_count"`
	ZoneNames        []string           `bson:"zone_names"`
	AddressCount     int64              `bson:"address_count"`
	DefaultAddress   customerAddress    `bson:"default_address"`
}

type customerAddress struct {
	BuildingInfo string `bson:"building_info"`
	Street       string `bson:"street"`
	Landmark     string `bson:"landmark"`
	City         string `bson:"city"`
	State        string `bson:"state"`
	Pincode      string `bson:"pincode"`
}

func NewCustomerBookingExecutor(db *mongo.Database) *CustomerBookingExecutor {
	return &CustomerBookingExecutor{db: db}
}

func (e *CustomerBookingExecutor) Key() string {
	return "customer_booking"
}

func (e *CustomerBookingExecutor) Version() int {
	return 1
}

func (e *CustomerBookingExecutor) Columns() []reports.Column {
	return withColumnDescriptions(customerBookingColumns)
}

func (e *CustomerBookingExecutor) Definition() reports.DefinitionMetadata {
	return reports.DefinitionMetadata{
		Filters: []reports.Filter{
			{Key: "start_date", Label: "Booking Start Date", Type: "date", Required: true},
			{Key: "end_date", Label: "Booking End Date", Type: "date", Required: true},
			{
				Key: "order_status", Label: "Booking Status", Type: "enum",
				Options: []reports.FilterOption{
					{Label: "All", Value: "all"},
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
			{
				Key: "customer_status", Label: "Customer Status", Type: "enum",
				Options: []reports.FilterOption{
					{Label: "All", Value: customerStatusAll},
					{Label: "Active", Value: customerStatusActive},
					{Label: "Inactive", Value: customerStatusInactive},
				},
			},
			{Key: "zone_ids", Label: "Saved Address Zones", Type: "object_id"},
			{Key: "customer_search", Label: "Customer Name, Phone, or Email", Type: "string"},
		},
		AllowedFormats: []string{"CSV", "XLSX"},
		DefaultFormat:  "XLSX",
	}
}

func (e *CustomerBookingExecutor) Validate(_ context.Context, req reports.Request) error {
	_, err := parseCustomerBookingFilters(req.Parameters)
	return err
}

func (e *CustomerBookingExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
	filters, err := parseCustomerBookingFilters(req.Parameters)
	if err != nil {
		return err
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, customerBookingPipeline(filters, req.Limit))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	header := make([]interface{}, len(customerBookingColumns))
	for index, column := range customerBookingColumns {
		header[index] = column.Label
	}
	if err := sink.WriteRow(header); err != nil {
		return err
	}

	for cursor.Next(ctx) {
		var row customerBookingRow
		if err := cursor.Decode(&row); err != nil {
			return err
		}
		if err := sink.WriteRow(row.values()); err != nil {
			return err
		}
	}

	return cursor.Err()
}

func parseCustomerBookingFilters(parameters map[string]interface{}) (customerBookingFilters, error) {
	if err := validateReportDateRange(parameters, parseDailySalesDate); err != nil {
		return customerBookingFilters{}, err
	}

	startDateText, _ := parameters["start_date"].(string)
	endDateText, _ := parameters["end_date"].(string)
	startDate, _ := parseDailySalesDate(startDateText, false)
	endDate, _ := parseDailySalesDate(endDateText, true)

	officeIDText, err := reportStringParam(parameters, "office_id")
	if err != nil {
		return customerBookingFilters{}, err
	}
	officeID, err := primitive.ObjectIDFromHex(officeIDText)
	if err != nil {
		return customerBookingFilters{}, fmt.Errorf("invalid office_id: %w", err)
	}

	filters := customerBookingFilters{
		StartDate:      startDate,
		EndDate:        endDate,
		OfficeID:       officeID,
		OrderStatus:    "completed",
		CustomerStatus: customerStatusAll,
	}

	if rawStatus, ok := parameters["order_status"]; ok {
		status, ok := rawStatus.(string)
		if !ok {
			return customerBookingFilters{}, fmt.Errorf("order_status must be a string")
		}
		status = strings.TrimSpace(status)
		if status == "" {
			return customerBookingFilters{}, fmt.Errorf("order_status must not be empty")
		}
		if status != "all" {
			if _, allowed := customerBookingStatuses[status]; !allowed {
				return customerBookingFilters{}, fmt.Errorf("invalid order_status: %s", status)
			}
		}
		filters.OrderStatus = status
	}

	if rawStatus, ok := parameters["customer_status"]; ok {
		status, ok := rawStatus.(string)
		if !ok {
			return customerBookingFilters{}, fmt.Errorf("customer_status must be a string")
		}
		status = strings.TrimSpace(status)
		switch status {
		case customerStatusAll, customerStatusActive, customerStatusInactive:
			filters.CustomerStatus = status
		default:
			return customerBookingFilters{}, fmt.Errorf("invalid customer_status: %s", status)
		}
	}

	zoneIDs, err := customerBookingZoneIDs(parameters)
	if err != nil {
		return customerBookingFilters{}, err
	}
	filters.ZoneIDs = zoneIDs

	if rawSearch, ok := parameters["customer_search"]; ok {
		search, ok := rawSearch.(string)
		if !ok {
			return customerBookingFilters{}, fmt.Errorf("customer_search must be a string")
		}
		filters.CustomerSearch = strings.TrimSpace(search)
	}

	return filters, nil
}

func customerBookingPipeline(filters customerBookingFilters, limit int) mongo.Pipeline {
	match := orderReportBaseMatch(
		filters.StartDate,
		filters.EndDate,
		filters.StartDate.Format("2006-01-02"),
		filters.EndDate.Format("2006-01-02"),
	)
	match["office_id"] = filters.OfficeID
	match["user_id"] = bson.M{"$ne": nil}
	if filters.OrderStatus != "all" {
		match["status"] = filters.OrderStatus
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$set", Value: bson.M{
			"report_booking_date": bson.M{"$ifNull": bson.A{
				"$service_date",
				bson.M{"$convert": bson.M{
					"input":   "$booking_info.date",
					"to":      "date",
					"onError": nil,
					"onNull":  nil,
				}},
			}},
		}}},
		{{Key: "$group", Value: bson.M{
			"_id":               "$user_id",
			"last_booking_date": bson.M{"$max": "$report_booking_date"},
			"booking_count":     bson.M{"$sum": 1},
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "users",
			"localField":   "_id",
			"foreignField": "_id",
			"as":           "customer",
		}}},
		{{Key: "$unwind", Value: "$customer"}},
		{{Key: "$match", Value: customerDocumentMatch(filters)}},
		{{Key: "$lookup", Value: bson.M{
			"from": "addresses",
			"let":  bson.M{"customer_id": "$_id"},
			"pipeline": bson.A{
				bson.M{"$match": bson.M{"$expr": bson.M{"$and": bson.A{
					bson.M{"$eq": bson.A{"$user_id", "$$customer_id"}},
					bson.M{"$ne": bson.A{"$is_deleted", true}},
				}}}},
				bson.M{"$sort": bson.M{"is_default": -1, "updated_at": -1}},
			},
			"as": "addresses",
		}}},
	}

	if len(filters.ZoneIDs) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{
			"addresses.zone_id": bson.M{"$in": filters.ZoneIDs},
		}}})
	}

	pipeline = append(pipeline,
		bson.D{{Key: "$lookup", Value: bson.M{
			"from":         "zones",
			"localField":   "addresses.zone_id",
			"foreignField": "_id",
			"as":           "zones",
		}}},
		bson.D{{Key: "$project", Value: bson.M{
			"customer_id":       "$_id",
			"customer_name":     bson.M{"$ifNull": bson.A{"$customer.full_name", ""}},
			"phone":             bson.M{"$ifNull": bson.A{"$customer.phone", ""}},
			"alternative_phone": bson.M{"$ifNull": bson.A{"$customer.alternative_number", ""}},
			"email":             bson.M{"$ifNull": bson.A{"$customer.email", ""}},
			"gender":            bson.M{"$ifNull": bson.A{"$customer.gender", ""}},
			"is_active":         bson.M{"$eq": bson.A{"$customer.is_active", true}},
			"registered_at":     "$customer.created_at",
			"last_booking_date": 1,
			"booking_count":     1,
			"zone_names":        "$zones.name",
			"address_count":     bson.M{"$size": "$addresses"},
			"default_address":   bson.M{"$arrayElemAt": bson.A{"$addresses", 0}},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{
			{Key: "last_booking_date", Value: -1},
			{Key: "customer_name", Value: 1},
			{Key: "customer_id", Value: 1},
		}}},
	)

	if limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: limit}})
	}
	return pipeline
}

func customerBookingZoneIDs(parameters map[string]interface{}) ([]primitive.ObjectID, error) {
	raw, exists := parameters["zone_ids"]
	if !exists {
		// Retain compatibility with templates created before zone multi-selection.
		raw, exists = parameters["zone_id"]
	}
	if !exists || raw == nil {
		return nil, nil
	}

	values := make([]string, 0)
	switch value := raw.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			values = append(values, value)
		}
	case []interface{}:
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("zone_ids must contain only strings")
			}
			if strings.TrimSpace(text) != "" {
				values = append(values, text)
			}
		}
	case []string:
		values = append(values, value...)
	default:
		return nil, fmt.Errorf("zone_ids must be an array of strings")
	}

	zoneIDs := make([]primitive.ObjectID, 0, len(values))
	seen := make(map[primitive.ObjectID]struct{}, len(values))
	for _, value := range values {
		zoneID, err := primitive.ObjectIDFromHex(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid zone_id: %w", err)
		}
		if _, exists := seen[zoneID]; exists {
			continue
		}
		seen[zoneID] = struct{}{}
		zoneIDs = append(zoneIDs, zoneID)
	}
	return zoneIDs, nil
}

func customerDocumentMatch(filters customerBookingFilters) bson.M {
	match := bson.M{
		"customer.is_deleted":          bson.M{"$ne": true},
		"customer.merged_into_user_id": bson.M{"$in": bson.A{nil, primitive.NilObjectID}},
	}

	switch filters.CustomerStatus {
	case customerStatusActive:
		match["customer.is_active"] = true
	case customerStatusInactive:
		match["customer.is_active"] = bson.M{"$ne": true}
	}

	if filters.CustomerSearch != "" {
		pattern := primitive.Regex{Pattern: regexp.QuoteMeta(filters.CustomerSearch), Options: "i"}
		match["$or"] = bson.A{
			bson.M{"customer.full_name": pattern},
			bson.M{"customer.phone": pattern},
			bson.M{"customer.alternative_number": pattern},
			bson.M{"customer.email": pattern},
		}
	}
	return match
}

func (row customerBookingRow) values() []interface{} {
	status := customerStatusInactive
	if row.IsActive {
		status = customerStatusActive
	}

	return []interface{}{
		row.CustomerID.Hex(),
		row.CustomerName,
		row.Phone,
		row.AlternativePhone,
		row.Email,
		row.Gender,
		status,
		formatReportDate(row.RegisteredAt),
		formatReportDate(row.LastBookingDate),
		row.BookingCount,
		strings.Join(uniqueSortedStrings(row.ZoneNames), ", "),
		row.AddressCount,
		formatCustomerAddress(row.DefaultAddress),
		row.DefaultAddress.City,
		row.DefaultAddress.State,
		row.DefaultAddress.Pincode,
	}
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func formatCustomerAddress(address customerAddress) string {
	parts := []string{
		address.BuildingInfo,
		address.Street,
		address.Landmark,
		address.City,
		address.State,
		address.Pincode,
	}
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, ", ")
}

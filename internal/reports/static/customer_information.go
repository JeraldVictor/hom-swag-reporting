package static

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type CustomerInformationExecutor struct {
	db *mongo.Database
}

type customerInformationFilters struct {
	StartDate       time.Time
	EndDate         time.Time
	OfficeID        primitive.ObjectID
	ZoneIDs         []primitive.ObjectID
	CustomerStatus  string
	CustomerSearch  string
	GetAllCustomers bool
	FilterByCreated bool
}

type customerInformationRow struct {
	CustomerID       primitive.ObjectID `bson:"_id"`
	CustomerName     string             `bson:"customer_name"`
	Phone            string             `bson:"phone"`
	Email            string             `bson:"email"`
	Gender           string             `bson:"gender"`
	IsActive         bool               `bson:"is_active"`
	CreatedAt        time.Time          `bson:"created_at"`
	LastBookingDate  time.Time          `bson:"last_booking_date"`
	ZoneNames        []string           `bson:"zones"`
	AddressCount     int64              `bson:"address_count"`
	DefaultAddress   customerAddress     `bson:"default_address"`
}

func NewCustomerInformationExecutor(db *mongo.Database) *CustomerInformationExecutor {
	return &CustomerInformationExecutor{db: db}
}

func (e *CustomerInformationExecutor) Key() string {
	return "customer_information"
}

func (e *CustomerInformationExecutor) Version() int {
	return 1
}

func (e *CustomerInformationExecutor) Columns() []reports.Column {
	return withColumnDescriptions(customerInformationColumns)
}

func (e *CustomerInformationExecutor) Definition() reports.DefinitionMetadata {
	return reports.DefinitionMetadata{
		Filters: []reports.Filter{
			{Key: "get_all_customers", Label: "Get All Customers", Type: "boolean", Required: false},
			{Key: "start_date", Label: "Created Start Date", Type: "date", Required: false},
			{Key: "end_date", Label: "Created End Date", Type: "date", Required: false},
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

func (e *CustomerInformationExecutor) Validate(_ context.Context, req reports.Request) error {
	_, err := parseCustomerInformationFilters(req.Parameters)
	return err
}

func (e *CustomerInformationExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
	filters, err := parseCustomerInformationFilters(req.Parameters)
	if err != nil {
		return err
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"office_id":  filters.OfficeID,
			"user_id":    bson.M{"$ne": nil},
			"is_deleted": bson.M{"$ne": true},
		}}},
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
			"_id":              "$user_id",
			"last_booking_date": bson.M{"$max": "$report_booking_date"},
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "users",
			"localField":   "_id",
			"foreignField": "_id",
			"as":           "customer",
		}}},
		{{Key: "$unwind", Value: bson.M{
			"path":                       "$customer",
			"preserveNullAndEmptyArrays": false,
		}}},
	}

	if filters.FilterByCreated {
		pipeline = append(pipeline, bson.D{{
			Key: "$match",
			Value: bson.M{
				"customer.created_at": bson.M{
					"$gte": filters.StartDate,
					"$lte": filters.EndDate,
				},
			},
		}})
	}

	pipeline = append(pipeline,
		bson.D{{Key: "$match", Value: customerDocumentMatch(customerBookingFilters{
			CustomerStatus: filters.CustomerStatus,
			CustomerSearch: filters.CustomerSearch,
		})}},
		bson.D{{Key: "$lookup", Value: bson.M{
			"from":         "addresses",
			"let":          bson.M{"customer_id": "$_id"},
			"pipeline": bson.A{
				bson.M{"$match": bson.M{"$expr": bson.M{"$and": bson.A{
					bson.M{"$eq": bson.A{"$user_id", "$$customer_id"}},
					bson.M{"$ne": bson.A{"$is_deleted", true}},
				}}}},
				bson.M{"$sort": bson.M{"is_default": -1, "updated_at": -1}},
			},
			"as": "addresses",
		}}},
	)

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
			"customer_id":      "$_id",
			"customer_name":    bson.M{"$ifNull": bson.A{"$customer.full_name", ""}},
			"phone":            bson.M{"$ifNull": bson.A{"$customer.phone", ""}},
			"email":            bson.M{"$ifNull": bson.A{"$customer.email", ""}},
			"gender":           bson.M{"$ifNull": bson.A{"$customer.gender", ""}},
			"is_active":        bson.M{"$eq": bson.A{"$customer.is_active", true}},
			"created_at":       "$customer.created_at",
			"last_booking_date": "$last_booking_date",
			"zones":            "$zones.name",
			"address_count":    bson.M{"$size": "$addresses"},
			"default_address":   bson.M{"$arrayElemAt": bson.A{"$addresses", 0}},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{
			{Key: "created_at", Value: -1},
			{Key: "customer_name", Value: 1},
			{Key: "customer_id", Value: 1},
		}}},
	)

	if req.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	header := make([]interface{}, len(customerInformationColumns))
	for index, column := range customerInformationColumns {
		header[index] = column.Label
	}
	if err := sink.WriteRow(header); err != nil {
		return err
	}

	for cursor.Next(ctx) {
		var row customerInformationRow
		if err := cursor.Decode(&row); err != nil {
			return err
		}
		if err := sink.WriteRow(row.values()); err != nil {
			return err
		}
	}
	return cursor.Err()
}

func parseCustomerInformationFilters(parameters map[string]interface{}) (customerInformationFilters, error) {
	officeIDText, err := reportStringParam(parameters, "office_id")
	if err != nil {
		return customerInformationFilters{}, err
	}
	officeID, err := primitive.ObjectIDFromHex(officeIDText)
	if err != nil {
		return customerInformationFilters{}, fmt.Errorf("invalid office_id: %w", err)
	}

	getAllCustomers := false
	if rawGetAll, exists := parameters["get_all_customers"]; exists {
		rawGetAllBool, ok := rawGetAll.(bool)
		if !ok {
			return customerInformationFilters{}, fmt.Errorf("get_all_customers must be a boolean")
		}
		getAllCustomers = rawGetAllBool
	}

	startDateText, hasStartDate := reportStringParamIfPresent(parameters, "start_date")
	endDateText, hasEndDate := reportStringParamIfPresent(parameters, "end_date")

	if !hasStartDate != !hasEndDate {
		return customerInformationFilters{}, fmt.Errorf("start_date and end_date must be provided together")
	}
	if !hasStartDate && !hasEndDate && !getAllCustomers {
		return customerInformationFilters{}, fmt.Errorf("start_date and end_date are required unless get_all_customers is true")
	}

	filters := customerInformationFilters{
		OfficeID:        officeID,
		GetAllCustomers: getAllCustomers,
		CustomerStatus:  customerStatusAll,
	}

	if hasStartDate {
		startDate, err := parseReportDate(startDateText, false)
		if err != nil {
			return customerInformationFilters{}, fmt.Errorf("invalid start_date: %w", err)
		}
		endDate, err := parseReportDate(endDateText, true)
		if err != nil {
			return customerInformationFilters{}, fmt.Errorf("invalid end_date: %w", err)
		}
		if endDate.Before(startDate) {
			return customerInformationFilters{}, fmt.Errorf("end_date must be greater than or equal to start_date")
		}
		filters.StartDate = startDate
		filters.EndDate = endDate
		filters.FilterByCreated = true
	}

	if rawStatus, ok := parameters["customer_status"]; ok {
		status, ok := rawStatus.(string)
		if !ok {
			return customerInformationFilters{}, fmt.Errorf("customer_status must be a string")
		}
		status = strings.TrimSpace(status)
		switch status {
		case customerStatusAll, customerStatusActive, customerStatusInactive:
			filters.CustomerStatus = status
		default:
			return customerInformationFilters{}, fmt.Errorf("invalid customer_status: %s", status)
		}
	}

	zoneIDs, err := customerBookingZoneIDs(parameters)
	if err != nil {
		return customerInformationFilters{}, err
	}
	filters.ZoneIDs = zoneIDs

	if rawSearch, ok := parameters["customer_search"]; ok {
		search, ok := rawSearch.(string)
		if !ok {
			return customerInformationFilters{}, fmt.Errorf("customer_search must be a string")
		}
		filters.CustomerSearch = strings.TrimSpace(search)
	}

	return filters, nil
}

func reportStringParamIfPresent(parameters map[string]interface{}, key string) (string, bool) {
	value, exists := parameters[key]
	if !exists || value == nil {
		return "", false
	}
	valueText, ok := value.(string)
	if !ok || strings.TrimSpace(valueText) == "" {
		return "", false
	}
	return valueText, true
}

func (row customerInformationRow) values() []interface{} {
	status := customerStatusInactive
	if row.IsActive {
		status = customerStatusActive
	}
	return []interface{}{
		row.CustomerID.Hex(),
		row.CustomerName,
		row.Phone,
		row.Email,
		row.Gender,
		status,
		formatReportDate(row.CreatedAt),
		formatReportDate(row.LastBookingDate),
		strings.Join(uniqueSortedStrings(row.ZoneNames), ", "),
		row.AddressCount,
		formatCustomerAddress(row.DefaultAddress),
		row.DefaultAddress.City,
		row.DefaultAddress.State,
		row.DefaultAddress.Pincode,
	}
}

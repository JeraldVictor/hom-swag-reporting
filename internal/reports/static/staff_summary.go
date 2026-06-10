package static

import (
	"context"
	"fmt"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type StaffSummaryExecutor struct {
	db *mongo.Database
}

func NewStaffSummaryExecutor(db *mongo.Database) *StaffSummaryExecutor {
	return &StaffSummaryExecutor{db: db}
}

func (e *StaffSummaryExecutor) Key() string {
	return "staff_summary"
}

func (e *StaffSummaryExecutor) Version() int {
	return 1
}

func (e *StaffSummaryExecutor) Validate(ctx context.Context, req reports.Request) error {
	if _, ok := req.Parameters["start_date"]; !ok {
		return fmt.Errorf("start_date is required")
	}
	if _, ok := req.Parameters["end_date"]; !ok {
		return fmt.Errorf("end_date is required")
	}
	return nil
}

func (e *StaffSummaryExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
	startDateStr := req.Parameters["start_date"].(string)
	endDateStr := req.Parameters["end_date"].(string)

	startDate, _ := time.Parse(time.RFC3339, startDateStr)
	endDate, _ := time.Parse(time.RFC3339, endDateStr)

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"is_deleted": false}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "roles",
			"localField":   "role_id",
			"foreignField": "_id",
			"as":           "role",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$role", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$project", Value: bson.M{
			"name":     1,
			"emp_code": 1,
			"role_name": "$role.name",
		}}},
	}

	if req.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("staffs").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	sink.WriteRow([]interface{}{"Employee Code", "Staff Name", "Role", "Leave Count", "Overtime Count"})

	for cursor.Next(ctx) {
		var staff struct {
			ID       interface{} `bson:"_id"`
			Name     string      `bson:"name"`
			EmpCode  string      `bson:"emp_code"`
			RoleName string      `bson:"role_name"`
		}
		if err := cursor.Decode(&staff); err != nil {
			return err
		}

		if staff.RoleName == "" {
			staff.RoleName = "N/A"
		}

		// Count leaves
		leaveCount, _ := e.db.Collection("leaverequests").CountDocuments(ctx, bson.M{
			"requester_id": staff.ID,
			"created_at":   bson.M{"$gte": startDate, "$lte": endDate},
			"status":       "approved",
			"is_deleted":   false,
		})

		// Count OT
		otCount, _ := e.db.Collection("overtimeentries").CountDocuments(ctx, bson.M{
			"requester_id": staff.ID,
			"created_at":   bson.M{"$gte": startDate, "$lte": endDate},
			"is_deleted":   false,
		})

		sink.WriteRow([]interface{}{
			staff.EmpCode,
			staff.Name,
			staff.RoleName,
			leaveCount,
			otCount,
		})
	}

	return nil
}

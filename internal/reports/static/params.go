package static

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func reportObjectID(parameters map[string]interface{}, key string) (primitive.ObjectID, bool) {
	value, ok := parameters[key].(string)
	if !ok || value == "" {
		return primitive.NilObjectID, false
	}
	id, err := primitive.ObjectIDFromHex(value)
	if err != nil {
		return primitive.NilObjectID, false
	}
	return id, true
}

func validateReportDateRange(
	parameters map[string]interface{},
	parser func(string, bool) (time.Time, error),
) error {
	startDateStr, err := reportStringParam(parameters, "start_date")
	if err != nil {
		return err
	}
	endDateStr, err := reportStringParam(parameters, "end_date")
	if err != nil {
		return err
	}

	startDate, err := parser(startDateStr, false)
	if err != nil {
		return fmt.Errorf("invalid start_date: %w", err)
	}
	endDate, err := parser(endDateStr, true)
	if err != nil {
		return fmt.Errorf("invalid end_date: %w", err)
	}
	if endDate.Before(startDate) {
		return fmt.Errorf("end_date must be greater than or equal to start_date")
	}
	return nil
}

func reportStringParam(parameters map[string]interface{}, key string) (string, error) {
	value, ok := parameters[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return text, nil
}

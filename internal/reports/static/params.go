package static

import "go.mongodb.org/mongo-driver/bson/primitive"

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

package static

import (
	"context"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type EarningsModeProvider interface {
	Mode(context.Context, primitive.ObjectID) (string, error)
}

func resolveEarningsMode(ctx context.Context, fallback string, provider EarningsModeProvider, officeID primitive.ObjectID) (string, error) {
	if provider != nil && !officeID.IsZero() {
		mode, err := provider.Mode(ctx, officeID)
		if err != nil {
			return "", err
		}
		if mode == "authoritative" {
			return mode, nil
		}
		return "shadow", nil
	}
	if fallback == "authoritative" {
		return fallback, nil
	}
	return "shadow", nil
}

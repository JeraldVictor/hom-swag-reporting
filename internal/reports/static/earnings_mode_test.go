package static

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type stubModeProvider struct {
	mode string
	err  error
}

func (s stubModeProvider) Mode(context.Context, primitive.ObjectID) (string, error) {
	return s.mode, s.err
}

func TestResolveEarningsMode(t *testing.T) {
	officeID := primitive.NewObjectID()
	tests := []struct {
		name     string
		fallback string
		provider EarningsModeProvider
		officeID primitive.ObjectID
		want     string
		wantErr  bool
	}{
		{name: "provider authoritative", provider: stubModeProvider{mode: "authoritative"}, officeID: officeID, want: "authoritative"},
		{name: "provider shadow", fallback: "authoritative", provider: stubModeProvider{mode: "shadow"}, officeID: officeID, want: "shadow"},
		{name: "provider unknown fails closed", provider: stubModeProvider{mode: "unknown"}, officeID: officeID, want: "shadow"},
		{name: "provider error", provider: stubModeProvider{err: errors.New("mode unavailable")}, officeID: officeID, wantErr: true},
		{name: "missing office uses authoritative fallback", fallback: "authoritative", provider: stubModeProvider{mode: "shadow"}, want: "authoritative"},
		{name: "invalid fallback fails closed", fallback: "invalid", want: "shadow"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mode, err := resolveEarningsMode(context.Background(), test.fallback, test.provider, test.officeID)
			if (err != nil) != test.wantErr || (!test.wantErr && mode != test.want) {
				t.Fatalf("mode=%q err=%v", mode, err)
			}
		})
	}
}

func TestModeProviderConstructors(t *testing.T) {
	provider := stubModeProvider{mode: "authoritative"}
	if NewRiderCommissionExecutorWithModeProvider(nil, "shadow", provider).modeProvider == nil {
		t.Fatal("rider provider missing")
	}
	if NewBeauticianCommissionExecutorWithModeProvider(nil, "shadow", provider).modeProvider == nil {
		t.Fatal("beautician provider missing")
	}
	if NewPetrolWeeklyExecutorWithModeProvider(nil, "shadow", provider).modeProvider == nil {
		t.Fatal("petrol provider missing")
	}
}

package static

import (
	"math"
	"regexp"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestLegacyPaymentExpressionsMatchNodeSplitPaymentParity(t *testing.T) {
	fixtures := []struct {
		name         string
		document     map[string]any
		cash         float64
		upi          float64
		online       float64
		bankTransfer float64
		total        float64
	}{
		{
			name: "actual paid remainder goes to bank transfer",
			document: map[string]any{
				"payment": map[string]any{
					"actual_paid_amount": 1200,
					"cod_amount":         200,
					"upi_amount":         300,
					"method":             "bank_transfer",
				},
			},
			cash:         200,
			upi:          300,
			bankTransfer: 700,
			total:        1200,
		},
		{
			name: "actual paid remainder goes to online for card payments",
			document: map[string]any{
				"payment": map[string]any{
					"actual_paid_amount": 1000,
					"method":             "card",
				},
			},
			online: 1000,
			total:  1000,
		},
		{
			name: "amount paid remainder goes to cash when method is cod",
			document: map[string]any{
				"payment": map[string]any{
					"amount_paid": 900,
					"upi_amount":  250,
					"method":      "cod",
				},
			},
			cash:  650,
			upi:   250,
			total: 900,
		},
		{
			name: "explicit online and bank fields are used when paid total is missing",
			document: map[string]any{
				"payment": map[string]any{
					"online_amount":        450,
					"bank_transfer_amount": 125,
				},
			},
			online:       450,
			bankTransfer: 125,
			total:        575,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			assertEvaluatedMoney(t, "cash", paymentCashExpr(), fixture.document, fixture.cash)
			assertEvaluatedMoney(t, "upi", paymentUpiExpr(), fixture.document, fixture.upi)
			assertEvaluatedMoney(t, "online", paymentOnlineExpr(), fixture.document, fixture.online)
			assertEvaluatedMoney(t, "bank_transfer", paymentBankTransferExpr(), fixture.document, fixture.bankTransfer)
			assertEvaluatedMoney(t, "total_received", paymentReceivedExpr(), fixture.document, fixture.total)
		})
	}
}

func TestPaymentHistoryExpressionsMatchNodeLabelAndRefundParity(t *testing.T) {
	document := map[string]any{
		"payment": map[string]any{
			"history": []any{
				map[string]any{"label": "UPI payment", "method": "UPI", "amount": 300},
				map[string]any{"label": "Bank transfer", "method": "Bank-Transfer", "amount": 200},
				map[string]any{"label": "Card payment", "method": "card", "amount": 150},
				map[string]any{"label": "Tip", "method": "upi", "amount": 50},
				map[string]any{"label": "Cancellation Fee", "method": "cash", "amount": 125},
				map[string]any{"label": "Cancellation-charge", "method": "cash", "amount": 75},
				map[string]any{"label": "Refund", "method": "upi", "amount": 25},
				map[string]any{"label": "Partial refund", "method": "upi", "amount": -40},
			},
		},
	}

	assertEvaluatedMoney(t, "cash", paymentCashExpr(), document, 0)
	assertEvaluatedMoney(t, "upi", paymentUpiExpr(), document, 300)
	assertEvaluatedMoney(t, "online", paymentOnlineExpr(), document, 150)
	assertEvaluatedMoney(t, "bank_transfer", paymentBankTransferExpr(), document, 200)
	assertEvaluatedMoney(t, "total_received", paymentReceivedExpr(), document, 650)
	assertEvaluatedMoney(t, "refund", paymentRefundExpr(), document, 65)
}

func assertEvaluatedMoney(t *testing.T, label string, expr any, document map[string]any, expected float64) {
	t.Helper()
	got := testNumber(t, evalTestExpr(t, expr, document, nil))
	if math.Abs(got-expected) > 0.001 {
		t.Fatalf("%s = %v, want %v", label, got, expected)
	}
}

func evalTestExpr(t *testing.T, expr any, document map[string]any, variables map[string]any) any {
	t.Helper()

	switch value := expr.(type) {
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	case float64:
		return value
	case string:
		if strings.HasPrefix(value, "$$") {
			return lookupTestVariable(variables, strings.TrimPrefix(value, "$$"))
		}
		if strings.HasPrefix(value, "$") {
			return lookupTestPath(document, strings.TrimPrefix(value, "$"))
		}
		return value
	case bson.A:
		values := make([]any, len(value))
		for index, item := range value {
			values[index] = evalTestExpr(t, item, document, variables)
		}
		return values
	case bson.M:
		if len(value) != 1 {
			return value
		}
		for op, raw := range value {
			switch op {
			case "$ifNull":
				items := raw.(bson.A)
				first := evalTestExpr(t, items[0], document, variables)
				if first != nil {
					return first
				}
				return evalTestExpr(t, items[1], document, variables)
			case "$gt":
				items := raw.(bson.A)
				return testNumber(t, evalTestExpr(t, items[0], document, variables)) > testNumber(t, evalTestExpr(t, items[1], document, variables))
			case "$lt":
				items := raw.(bson.A)
				return testNumber(t, evalTestExpr(t, items[0], document, variables)) < testNumber(t, evalTestExpr(t, items[1], document, variables))
			case "$ne":
				items := raw.(bson.A)
				return evalTestExpr(t, items[0], document, variables) != evalTestExpr(t, items[1], document, variables)
			case "$eq":
				items := raw.(bson.A)
				return evalTestExpr(t, items[0], document, variables) == evalTestExpr(t, items[1], document, variables)
			case "$and":
				for _, item := range raw.(bson.A) {
					if !testBool(t, evalTestExpr(t, item, document, variables)) {
						return false
					}
				}
				return true
			case "$or":
				for _, item := range raw.(bson.A) {
					if testBool(t, evalTestExpr(t, item, document, variables)) {
						return true
					}
				}
				return false
			case "$size":
				items, ok := evalTestExpr(t, raw, document, variables).([]any)
				if !ok {
					if bsonItems, ok := evalTestExpr(t, raw, document, variables).(bson.A); ok {
						return float64(len(bsonItems))
					}
					return float64(0)
				}
				return float64(len(items))
			case "$cond":
				items := raw.(bson.A)
				if testBool(t, evalTestExpr(t, items[0], document, variables)) {
					return evalTestExpr(t, items[1], document, variables)
				}
				return evalTestExpr(t, items[2], document, variables)
			case "$add":
				total := 0.0
				for _, item := range raw.(bson.A) {
					total += testNumber(t, evalTestExpr(t, item, document, variables))
				}
				return total
			case "$reduce":
				spec := raw.(bson.M)
				input := evalTestArray(t, evalTestExpr(t, spec["input"], document, variables))
				accumulator := evalTestExpr(t, spec["initialValue"], document, variables)
				for _, item := range input {
					nextVariables := cloneTestVariables(variables)
					nextVariables["value"] = accumulator
					nextVariables["this"] = item
					accumulator = evalTestExpr(t, spec["in"], document, nextVariables)
				}
				return accumulator
			case "$max":
				maximum := math.Inf(-1)
				for _, item := range raw.(bson.A) {
					number := testNumber(t, evalTestExpr(t, item, document, variables))
					if number > maximum {
						maximum = number
					}
				}
				return maximum
			case "$subtract":
				items := raw.(bson.A)
				return testNumber(t, evalTestExpr(t, items[0], document, variables)) - testNumber(t, evalTestExpr(t, items[1], document, variables))
			case "$multiply":
				product := 1.0
				for _, item := range raw.(bson.A) {
					product *= testNumber(t, evalTestExpr(t, item, document, variables))
				}
				return product
			case "$divide":
				items := raw.(bson.A)
				denominator := testNumber(t, evalTestExpr(t, items[1], document, variables))
				if denominator == 0 {
					return float64(0)
				}
				return testNumber(t, evalTestExpr(t, items[0], document, variables)) / denominator
			case "$round":
				items := raw.(bson.A)
				number := testNumber(t, evalTestExpr(t, items[0], document, variables))
				places := int(testNumber(t, evalTestExpr(t, items[1], document, variables)))
				scale := math.Pow(10, float64(places))
				return math.Round(number*scale) / scale
			case "$toLower":
				return strings.ToLower(testString(evalTestExpr(t, raw, document, variables)))
			case "$trim":
				spec := raw.(bson.M)
				return strings.TrimSpace(testString(evalTestExpr(t, spec["input"], document, variables)))
			case "$replaceAll":
				spec := raw.(bson.M)
				return strings.ReplaceAll(
					testString(evalTestExpr(t, spec["input"], document, variables)),
					testString(evalTestExpr(t, spec["find"], document, variables)),
					testString(evalTestExpr(t, spec["replacement"], document, variables)),
				)
			case "$abs":
				return math.Abs(testNumber(t, evalTestExpr(t, raw, document, variables)))
			case "$in":
				items := raw.(bson.A)
				needle := evalTestExpr(t, items[0], document, variables)
				for _, candidate := range items[1].(bson.A) {
					if needle == candidate {
						return true
					}
				}
				return false
			case "$not":
				items := raw.(bson.A)
				return !testBool(t, evalTestExpr(t, items[0], document, variables))
			case "$regexMatch":
				spec := raw.(bson.M)
				input := testString(evalTestExpr(t, spec["input"], document, variables))
				regex := testString(evalTestExpr(t, spec["regex"], document, variables))
				matched, err := regexp.MatchString(regex, input)
				if err != nil {
					t.Fatalf("invalid regex %q: %v", regex, err)
				}
				return matched
			default:
				t.Fatalf("unsupported test expression operator %s", op)
			}
		}
	}

	return expr
}

func lookupTestPath(document map[string]any, path string) any {
	var current any = document
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func lookupTestVariable(variables map[string]any, path string) any {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil
	}
	var current any
	if variables != nil {
		current = variables[parts[0]]
	}
	for _, part := range parts[1:] {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func cloneTestVariables(variables map[string]any) map[string]any {
	next := map[string]any{}
	for key, value := range variables {
		next[key] = value
	}
	return next
}

func evalTestArray(t *testing.T, value any) []any {
	t.Helper()
	switch items := value.(type) {
	case nil:
		return nil
	case []any:
		return items
	case bson.A:
		return []any(items)
	default:
		t.Fatalf("expected array value, got %#v", value)
	}
	return nil
}

func testNumber(t *testing.T, value any) float64 {
	t.Helper()
	switch number := value.(type) {
	case nil:
		return 0
	case int:
		return float64(number)
	case int32:
		return float64(number)
	case int64:
		return float64(number)
	case float64:
		return number
	default:
		t.Fatalf("expected numeric value, got %#v", value)
	}
	return 0
}

func testBool(t *testing.T, value any) bool {
	t.Helper()
	boolean, ok := value.(bool)
	if !ok {
		t.Fatalf("expected bool value, got %#v", value)
	}
	return boolean
}

func testString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

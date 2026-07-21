package earnings

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func signTestToken(t *testing.T, secret, algorithm string, claims map[string]interface{}) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": algorithm, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature.Sum(nil))
}

func validTestClaims() map[string]interface{} {
	return map[string]interface{}{
		"payload": map[string]interface{}{
			"sub":         "507f1f77bcf86cd799439011",
			"office_id":   "507f1f77bcf86cd799439012",
			"is_admin":    true,
			"office_ids":  []string{"507f1f77bcf86cd799439012"},
			"permissions": []string{"ledger.read", "ledger.payout", "ledger.rebuild", "ledger.cutover"},
		},
		"exp": time.Now().Add(time.Hour).Unix(),
	}
}

func TestVerifyAdminToken(t *testing.T) {
	const secret = "test-secret"
	token := signTestToken(t, secret, "HS256", validTestClaims())
	principal, err := VerifyAdminToken("Bearer "+token, secret)
	if err != nil {
		t.Fatalf("VerifyAdminToken() error = %v", err)
	}
	if principal.StaffID != "507f1f77bcf86cd799439011" || !principal.HasPermission("ledger.read") {
		t.Fatalf("unexpected principal: %#v", principal)
	}
	if err := principal.CanAccessOffice("507f1f77bcf86cd799439012"); err != nil {
		t.Fatalf("expected selected office access: %v", err)
	}
}

func TestVerifyAdminTokenRejectsInvalidTokens(t *testing.T) {
	const secret = "test-secret"
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{name: "missing bearer", header: "", want: "bearer token"},
		{name: "invalid jwt segments", header: "Bearer one.two", want: "invalid JWT"},
		{name: "invalid encoded header", header: "Bearer %.e30.signature", want: "header"},
		{name: "invalid header json", header: "Bearer " + base64.RawURLEncoding.EncodeToString([]byte(`{`)) + ".e30.signature", want: "HS256"},
		{name: "wrong algorithm", header: "Bearer " + signTestToken(t, secret, "HS512", validTestClaims()), want: "HS256"},
		{name: "wrong signature", header: "Bearer " + signTestToken(t, "other-secret", "HS256", validTestClaims()), want: "signature"},
	}
	valid := signTestToken(t, secret, "HS256", validTestClaims())
	validParts := strings.Split(valid, ".")
	tests = append(tests,
		struct{ name, header, want string }{name: "invalid encoded signature", header: "Bearer " + validParts[0] + "." + validParts[1] + ".%", want: "signature"},
	)
	invalidPayloadUnsigned := validParts[0] + ".%"
	invalidPayloadSignature := hmac.New(sha256.New, []byte(secret))
	_, _ = invalidPayloadSignature.Write([]byte(invalidPayloadUnsigned))
	tests = append(tests, struct{ name, header, want string }{
		name: "invalid encoded payload", header: "Bearer " + invalidPayloadUnsigned + "." + base64.RawURLEncoding.EncodeToString(invalidPayloadSignature.Sum(nil)), want: "payload",
	})
	badClaimsToken := signTestToken(t, secret, "HS256", map[string]interface{}{"payload": "bad", "exp": time.Now().Add(time.Hour).Unix()})
	tests = append(tests, struct{ name, header, want string }{name: "invalid claims", header: "Bearer " + badClaimsToken, want: "claims"})
	missingSubject := validTestClaims()
	missingSubject["payload"].(map[string]interface{})["sub"] = ""
	tests = append(tests, struct{ name, header, want string }{name: "missing subject", header: "Bearer " + signTestToken(t, secret, "HS256", missingSubject), want: "subject"})
	expired := validTestClaims()
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	tests = append(tests, struct {
		name   string
		header string
		want   string
	}{name: "expired", header: "Bearer " + signTestToken(t, secret, "HS256", expired), want: "expired"})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := VerifyAdminToken(test.header, secret)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestVerifyAdminTokenRejectsMissingSecret(t *testing.T) {
	if _, err := VerifyAdminToken("Bearer token", ""); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrincipalPermissionAndOfficeScope(t *testing.T) {
	principal := Principal{
		IsAdmin:     true,
		OfficeIDs:   []string{"office-a"},
		Permissions: []string{"ledger.*"},
	}
	if !principal.HasPermission("ledger.rebuild") {
		t.Fatal("ledger.* should grant ledger.rebuild")
	}
	if err := principal.CanAccessOffice("office-b"); err == nil {
		t.Fatal("assigned admin should not access an unassigned office")
	}
	principal.OfficeIDs = nil
	if err := principal.CanAccessOffice("office-b"); err != nil {
		t.Fatalf("all-office admin should have access: %v", err)
	}
	if err := (Principal{}).CanAccessOffice(""); err == nil {
		t.Fatal("empty office should be rejected")
	}
	principal.OfficeIDs = []string{"office-a", "office-b"}
	if err := principal.CanAccessOffice("office-b"); err != nil {
		t.Fatalf("assigned office should be allowed: %v", err)
	}
}

func TestAdminPermissionOverrideMatchesAdminApp(t *testing.T) {
	if !(Principal{IsAdmin: true}).HasPermission("ledger.cutover") {
		t.Fatal("administrator must retain full ledger control with a stale permission snapshot")
	}
	if (Principal{}).HasPermission("ledger.cutover") {
		t.Fatal("non-admin without the permission must remain denied")
	}
}

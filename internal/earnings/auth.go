package earnings

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Principal struct {
	StaffID     string
	OfficeID    string
	IsAdmin     bool
	OfficeIDs   []string
	Permissions []string
}

type tokenClaims struct {
	Payload struct {
		Sub         string   `json:"sub"`
		OfficeID    string   `json:"office_id"`
		IsAdmin     bool     `json:"is_admin"`
		OfficeIDs   []string `json:"office_ids"`
		Permissions []string `json:"permissions"`
	} `json:"payload"`
	ExpiresAt int64 `json:"exp"`
}

type tokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

func VerifyAdminToken(header string, secret string) (Principal, error) {
	if strings.TrimSpace(secret) == "" {
		return Principal{}, errors.New("JWT_SECRET is not configured")
	}
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return Principal{}, errors.New("bearer token is required")
	}
	tokenParts := strings.Split(parts[1], ".")
	if len(tokenParts) != 3 {
		return Principal{}, errors.New("invalid JWT")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(tokenParts[0])
	if err != nil {
		return Principal{}, errors.New("invalid JWT header")
	}
	var tokenHeader tokenHeader
	if err := json.Unmarshal(headerJSON, &tokenHeader); err != nil || tokenHeader.Algorithm != "HS256" {
		return Principal{}, errors.New("JWT must use HS256")
	}

	signingInput := tokenParts[0] + "." + tokenParts[1]
	expected := hmac.New(sha256.New, []byte(secret))
	_, _ = expected.Write([]byte(signingInput))
	actual, err := base64.RawURLEncoding.DecodeString(tokenParts[2])
	if err != nil || !hmac.Equal(actual, expected.Sum(nil)) {
		return Principal{}, errors.New("invalid JWT signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(tokenParts[1])
	if err != nil {
		return Principal{}, errors.New("invalid JWT payload")
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Principal{}, errors.New("invalid JWT claims")
	}
	if claims.ExpiresAt <= time.Now().Unix() {
		return Principal{}, errors.New("JWT has expired")
	}
	if claims.Payload.Sub == "" {
		return Principal{}, errors.New("JWT subject is missing")
	}

	return Principal{
		StaffID: claims.Payload.Sub, OfficeID: claims.Payload.OfficeID,
		IsAdmin: claims.Payload.IsAdmin, OfficeIDs: claims.Payload.OfficeIDs,
		Permissions: claims.Payload.Permissions,
	}, nil
}

func (p Principal) HasPermission(required string) bool {
	for _, permission := range p.Permissions {
		if permission == required || permission == "*:*" || permission == "*.*" {
			return true
		}
		for _, separator := range []string{":", "."} {
			parts := strings.SplitN(required, separator, 2)
			if len(parts) == 2 && (permission == parts[0]+separator+"*" || permission == parts[0]+separator+"**") {
				return true
			}
		}
	}
	return false
}

func (p Principal) CanAccessOffice(officeID string) error {
	if officeID == "" {
		return errors.New("office_id is required")
	}
	if p.OfficeID == officeID {
		return nil
	}
	if !p.IsAdmin {
		return fmt.Errorf("office %s is outside the selected office scope", officeID)
	}
	if len(p.OfficeIDs) == 0 {
		return nil
	}
	for _, allowed := range p.OfficeIDs {
		if allowed == officeID {
			return nil
		}
	}
	return fmt.Errorf("office %s is not assigned to this administrator", officeID)
}

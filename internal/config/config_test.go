package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetJWTSecret(t *testing.T) {
	t.Run("direct environment wins", func(t *testing.T) {
		t.Setenv("JWT_SECRET", " direct-secret ")
		t.Setenv("JWT_SECRET_ENV_FILE", "missing")
		if got := getJWTSecret(); got != "direct-secret" {
			t.Fatalf("secret = %q", got)
		}
	})

	t.Run("reads only the shared dotenv secret", func(t *testing.T) {
		t.Setenv("JWT_SECRET", "")
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte("JWT_SECRET=shared-secret\nPORT=9999\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("JWT_SECRET_ENV_FILE", path)
		if got := getJWTSecret(); got != "shared-secret" {
			t.Fatalf("secret = %q", got)
		}
	})

	t.Run("missing configuration stays fail closed", func(t *testing.T) {
		t.Setenv("JWT_SECRET", "")
		t.Setenv("JWT_SECRET_ENV_FILE", "")
		if got := getJWTSecret(); got != "" {
			t.Fatalf("secret = %q", got)
		}
		t.Setenv("JWT_SECRET_ENV_FILE", filepath.Join(t.TempDir(), "missing"))
		if got := getJWTSecret(); got != "" {
			t.Fatalf("missing file secret = %q", got)
		}
	})
}

func TestLoadNonTransactionalWritesDefaultsOffAndRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("EARNINGS_ALLOW_NON_TRANSACTIONAL_WRITES", "")
	if Load().AllowNonTransactionalWrites {
		t.Fatal("non-transactional writes must default to disabled")
	}

	t.Setenv("EARNINGS_ALLOW_NON_TRANSACTIONAL_WRITES", "true")
	if !Load().AllowNonTransactionalWrites {
		t.Fatal("expected explicit local opt-in to enable non-transactional writes")
	}
}

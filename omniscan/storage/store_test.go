package storage

import (
	"os"
	"testing"
)

func TestSQLiteQuotaStore(t *testing.T) {
	dbFile := "test_quota.db"
	defer os.Remove(dbFile)

	store, err := NewSQLiteQuotaStore(dbFile)
	if err != nil {
		t.Fatalf("failed to create QuotaStore: %v", err)
	}
	defer store.Close()

	userID := "user_test_123"
	limit := 5

	for i := 1; i <= 5; i++ {
		allowed, count, err := store.CheckAndIncrementQuota(userID, limit)
		if err != nil {
			t.Fatalf("unexpected error on scan %d: %v", i, err)
		}
		if !allowed {
			t.Errorf("expected scan %d to be allowed", i)
		}
		if count != i {
			t.Errorf("expected count %d, got %d", i, count)
		}
	}

	allowed, count, err := store.CheckAndIncrementQuota(userID, limit)
	if err != nil {
		t.Fatalf("unexpected error on 6th scan: %v", err)
	}
	if allowed {
		t.Errorf("expected 6th scan to be rejected")
	}
	if count != 5 {
		t.Errorf("expected count to stay at 5, got %d", count)
	}

	used, remaining, err := store.GetQuota(userID, limit)
	if err != nil {
		t.Fatalf("GetQuota error: %v", err)
	}
	if used != 5 || remaining != 0 {
		t.Errorf("expected used 5, remaining 0, got used %d, remaining %d", used, remaining)
	}

	if err := store.RefundQuota(userID); err != nil {
		t.Fatalf("RefundQuota error: %v", err)
	}

	used, remaining, err = store.GetQuota(userID, limit)
	if err != nil {
		t.Fatalf("GetQuota after refund error: %v", err)
	}
	if used != 4 || remaining != 1 {
		t.Errorf("expected used 4, remaining 1 after refund, got used %d, remaining %d", used, remaining)
	}
}

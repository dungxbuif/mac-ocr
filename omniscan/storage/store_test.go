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
	scanLimit, ocrLimit := 3, 5

	// *scan counter: 3 increments allowed, 4th rejected.
	for i := 1; i <= scanLimit; i++ {
		allowed, count, err := store.CheckAndIncrementScanQuota(userID, scanLimit)
		if err != nil {
			t.Fatalf("scan %d: %v", i, err)
		}
		if !allowed || count != i {
			t.Errorf("scan %d: want allowed=true count=%d, got allowed=%v count=%d", i, i, allowed, count)
		}
	}
	if allowed, count, err := store.CheckAndIncrementScanQuota(userID, scanLimit); err != nil || allowed || count != scanLimit {
		t.Errorf("scan over-limit: want allowed=false count=%d, got allowed=%v count=%d err=%v", scanLimit, allowed, count, err)
	}

	// *ocr counter is INDEPENDENT: still 0, all 5 increments must succeed.
	for i := 1; i <= ocrLimit; i++ {
		allowed, count, err := store.CheckAndIncrementOCRQuota(userID, ocrLimit)
		if err != nil {
			t.Fatalf("ocr %d: %v", i, err)
		}
		if !allowed || count != i {
			t.Errorf("ocr %d: want allowed=true count=%d, got allowed=%v count=%d", i, i, allowed, count)
		}
	}
	if allowed, _, err := store.CheckAndIncrementOCRQuota(userID, ocrLimit); err != nil || allowed {
		t.Errorf("ocr over-limit: want allowed=false, got allowed=%v err=%v", allowed, err)
	}

	// GetQuota reports both counters independently.
	scanUsed, scanRem, ocrUsed, ocrRem, err := store.GetQuota(userID, scanLimit, ocrLimit)
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if scanUsed != scanLimit || scanRem != 0 {
		t.Errorf("scan: want used=%d rem=0, got used=%d rem=%d", scanLimit, scanUsed, scanRem)
	}
	if ocrUsed != ocrLimit || ocrRem != 0 {
		t.Errorf("ocr: want used=%d rem=0, got used=%d rem=%d", ocrLimit, ocrUsed, ocrRem)
	}

	// Refund only the scan counter; OCR stays unchanged.
	if err := store.RefundScanQuota(userID); err != nil {
		t.Fatalf("RefundScanQuota: %v", err)
	}
	scanUsed, scanRem, ocrUsed, ocrRem, _ = store.GetQuota(userID, scanLimit, ocrLimit)
	if scanUsed != scanLimit-1 || scanRem != 1 {
		t.Errorf("after scan refund: want used=%d rem=1, got used=%d rem=%d", scanLimit-1, scanUsed, scanRem)
	}
	if ocrUsed != ocrLimit || ocrRem != 0 {
		t.Errorf("ocr unchanged: want used=%d rem=0, got used=%d rem=%d", ocrLimit, ocrUsed, ocrRem)
	}

	// Refund OCR counter; scan stays unchanged.
	if err := store.RefundOCRQuota(userID); err != nil {
		t.Fatalf("RefundOCRQuota: %v", err)
	}
	scanUsed, _, ocrUsed, ocrRem, _ = store.GetQuota(userID, scanLimit, ocrLimit)
	if scanUsed != scanLimit-1 {
		t.Errorf("scan unchanged after OCR refund: want used=%d, got %d", scanLimit-1, scanUsed)
	}
	if ocrUsed != ocrLimit-1 || ocrRem != 1 {
		t.Errorf("ocr after refund: want used=%d rem=1, got used=%d rem=%d", ocrLimit-1, ocrUsed, ocrRem)
	}
}

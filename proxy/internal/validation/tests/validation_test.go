package tests

import (
	"testing"

	"macocr/proxy/internal/validation"
)

func TestValidateKeyOK(t *testing.T) {
	fe := validation.FieldErrors{}
	fe.ValidateKey("key", "uploads/01ABCDEFGHJKLMNPQRSTVWXYZ/object")
	if fe.Has() {
		t.Fatalf("expected valid key, got %v", fe)
	}
}

func TestValidateKeyEmpty(t *testing.T) {
	fe := validation.FieldErrors{}
	fe.ValidateKey("key", "")
	if !fe.Has() {
		t.Fatal("expected error for empty key")
	}
	if _, ok := fe["key"]; !ok {
		t.Fatalf("expected field error for key, got %v", fe)
	}
}

func TestValidateKeyControlChar(t *testing.T) {
	fe := validation.FieldErrors{}
	fe.ValidateKey("key", "bad\x00key")
	if !fe.Has() {
		t.Fatal("expected error for control char")
	}
}

func TestValidateKeyTooLong(t *testing.T) {
	long := make([]byte, 1100)
	for i := range long {
		long[i] = 'a'
	}
	fe := validation.FieldErrors{}
	fe.ValidateKey("key", string(long))
	if !fe.Has() {
		t.Fatal("expected error for too-long key")
	}
}

func TestIsULID(t *testing.T) {
	if !validation.IsULID("0123456789ABCDEFGHJKMNPQRS") {
		t.Fatal("expected valid ULID")
	}
	if validation.IsULID("short") {
		t.Fatal("did not expect short string to be a ULID")
	}
	if validation.IsULID("0123456789ABCDEFGHJKLNPQRS") { // contains invalid 'L'
		t.Fatal("did not expect string with excluded char to be a ULID")
	}
}

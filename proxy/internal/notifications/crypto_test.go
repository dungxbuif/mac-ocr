package notifications

import "testing"

func TestSecretCipherRoundTrip(t *testing.T) {
	cipher, err := NewSecretCipher("a-long-local-encryption-key")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("my-hmac-secret-123")
	if err != nil {
		t.Fatal(err)
	}
	if string(encrypted) == "my-hmac-secret-123" {
		t.Fatal("secret was stored as plaintext")
	}
	plain, err := cipher.Decrypt(encrypted)
	if err != nil || plain != "my-hmac-secret-123" {
		t.Fatalf("round trip failed: %q %v", plain, err)
	}
}

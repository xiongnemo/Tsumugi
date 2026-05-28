package secure

import (
	"bytes"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	cipher, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	ciphertext, err := cipher.Seal("messages", "row-1", "text", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("hello")) {
		t.Fatalf("ciphertext contains plaintext: %q", ciphertext)
	}

	plaintext, err := cipher.Open("messages", "row-1", "text", ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "hello" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestCipherBindsAAD(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	cipher, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Seal("messages", "row-1", "text", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cipher.Open("messages", "row-2", "text", ciphertext); err == nil {
		t.Fatal("expected authentication failure with different row id")
	}
}

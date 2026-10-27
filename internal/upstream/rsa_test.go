package upstream

import (
	"strings"
	"testing"
)

func TestEncryptRawKnownVector(t *testing.T) {
	// RSA toy example: p=61, q=53, n=3233, e=17, plaintext 65 -> 2790.
	got, err := EncryptRaw("A", "CA1", "11")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("0", 253) + "ae6"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEncryptPasswordFormat(t *testing.T) {
	first, err := EncryptPassword("secret-pass")
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncryptPassword("secret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 256 {
		t.Fatalf("length = %d, want 256", len(first))
	}
	if first != second {
		t.Fatal("raw RSA encryption should be deterministic")
	}
	if strings.Contains(first, "secret") {
		t.Fatal("output contains plaintext")
	}
	if _, err := EncryptPassword(""); err == nil {
		t.Fatal("empty password accepted")
	}
	// 反转后再加密：同一个明文反转后不同，结果必须不同。
	reversed, err := EncryptPassword("ssap-terces")
	if err != nil {
		t.Fatal(err)
	}
	if reversed == first {
		t.Fatal("password reversal not applied")
	}
}

func TestEncryptRawRejectsBadKey(t *testing.T) {
	cases := [][3]string{
		{"A", "", "11"},
		{"A", "zz", "11"},
		{"A", "CA1", ""},
		{"", "CA1", "11"},
	}
	for i, c := range cases {
		if _, err := EncryptRaw(c[0], c[1], c[2]); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

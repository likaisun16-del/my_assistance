package jwt

import "testing"

func TestSignAndParse_RoundTrip(t *testing.T) {
	j := New("test-secret", 24)
	tok, err := j.Sign(42)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := j.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if uid != 42 {
		t.Fatalf("uid = %d, want 42", uid)
	}
}

func TestParse_BadToken(t *testing.T) {
	j := New("test-secret", 24)
	if _, err := j.Parse("not-a-token"); err == nil {
		t.Fatal("expect error for invalid token")
	}
}

func TestParse_WrongSecret(t *testing.T) {
	j1 := New("secret-a", 24)
	j2 := New("secret-b", 24)
	tok, _ := j1.Sign(1)
	if _, err := j2.Parse(tok); err == nil {
		t.Fatal("expect error when secret mismatches")
	}
}

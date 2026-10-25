package identity

import "testing"

func TestHashAndVerifyPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("expected password to verify")
	}
}

func TestVerifyPassword_WrongPasswordFails(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := VerifyPassword(hash, "wrong password")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("expected wrong password to fail verification")
	}
}

func TestHashPassword_DistinctSaltsProduceDistinctHashes(t *testing.T) {
	h1, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("expected distinct salts to produce distinct encoded hashes")
	}
}

func TestValidatePasswordPolicy(t *testing.T) {
	if err := ValidatePasswordPolicy("short"); err == nil {
		t.Fatal("expected short password to fail policy")
	}
	if err := ValidatePasswordPolicy("a sufficiently long passphrase"); err != nil {
		t.Fatalf("expected long passphrase to pass policy, got: %v", err)
	}
}

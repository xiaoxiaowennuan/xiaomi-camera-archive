package auth

import "testing"

func TestPasswordValidation(t *testing.T) {
	if err := ValidatePassword("Abcd1234!"); err != nil {
		t.Fatalf("expected configured password to be accepted: %v", err)
	}
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("expected short password to be rejected")
	}
}

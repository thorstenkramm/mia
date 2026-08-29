package identity

import "testing"

func TestUsername(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{{"Ada-1", true}, {"ab", false}, {"-ada", false}, {"ada_", false}, {"ada ü", false}}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			_, err := Username(test.value)
			if (err == nil) != test.valid {
				t.Fatalf("Username(%q) error = %v", test.value, err)
			}
		})
	}
}

func TestUsernameRejectsNonASCIILowByteCollision(t *testing.T) {
	if _, err := Username("a\u0130a"); err == nil {
		t.Fatal("Username accepted U+0130, whose low byte is ASCII '0'")
	}
}

func TestCountryUsesISO3166CountryCodes(t *testing.T) {
	if value, err := Country("de"); err != nil || value != "DE" {
		t.Fatalf("Country(DE) = %q, %v", value, err)
	}
	if _, err := Country("ZZ"); err == nil {
		t.Fatal("Country accepted unassigned ZZ")
	}
}

func TestTextNormalizesAndRepresentsAbsence(t *testing.T) {
	value, err := Text(" \r\nHello\rworld ", 20, 50)
	if err != nil || value == nil || *value != "Hello\nworld" {
		t.Fatalf("Text result = %v, %v", value, err)
	}
	empty, err := Text(" \t ", 20, 50)
	if err != nil || empty != nil {
		t.Fatalf("empty Text result = %v, %v", empty, err)
	}
}

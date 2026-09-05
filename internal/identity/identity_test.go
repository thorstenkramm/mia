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
	for _, value := range []string{"de-DE", "ZZ"} {
		t.Run(value, func(t *testing.T) {
			if _, err := Country(value); err == nil || err.Error() != "use an uppercase ISO 3166-1 alpha-2 country code, like DE, US, AR" {
				t.Fatalf("Country(%q) error = %v", value, err)
			}
		})
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

func TestLanguageAndTimeZoneRejectUnsafeValues(t *testing.T) {
	for _, value := range []string{"x-private", ""} {
		if _, err := Language(value); err == nil {
			t.Fatalf("Language(%q) was accepted", value)
		}
	}
	for _, value := range []string{"", "Local", "+01:00", "-01:00"} {
		if _, err := TimeZone(value); err == nil {
			t.Fatalf("TimeZone(%q) was accepted", value)
		}
	}
}

func TestE164RequiresFullStrictInternationalForm(t *testing.T) {
	for value, valid := range map[string]bool{
		"+12345678": true,
		"+1234567":  false,
		"+02345678": false,
		"12345678":  false,
		"+123 4567": false,
	} {
		t.Run(value, func(t *testing.T) {
			_, err := E164(value)
			if (err == nil) != valid {
				t.Fatalf("E164(%q) error = %v", value, err)
			}
		})
	}
}

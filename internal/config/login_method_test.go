package config

import "testing"

func TestParseLoginMethod(t *testing.T) {
	cases := map[string]LoginMethod{
		"qr":       LoginQR,
		"QR":       LoginQR,
		" qr ":     LoginQR,
		"phone":    LoginPhone,
		"":         LoginPhone,
		"nonsense": LoginPhone,
	}
	for input, want := range cases {
		if got := ParseLoginMethod(input); got != want {
			t.Errorf("ParseLoginMethod(%q) = %q, want %q", input, got, want)
		}
	}
}

// QR login has nothing to type, so onboarding must not stop and ask for a phone number.
func TestNeedsOnboardingSkipsPhoneForQR(t *testing.T) {
	cfg := Config{AuthMode: AuthUser, LoginMethod: LoginQR, APIID: 1, APIHash: "hash"}

	if cfg.NeedsOnboarding() {
		t.Fatal("QR login needs no phone number")
	}

	cfg.LoginMethod = LoginPhone
	if !cfg.NeedsOnboarding() {
		t.Fatal("phone login without a number still needs onboarding")
	}
}

// A QR session is an ordinary user session: it must keep user capabilities, which is the whole
// reason LoginMethod is a separate axis from AuthMode rather than a third mode.
func TestQRLoginIsStillUserMode(t *testing.T) {
	cfg := Config{AuthMode: AuthUser, LoginMethod: LoginQR, APIID: 1, APIHash: "hash"}

	if cfg.AuthMode != AuthUser {
		t.Fatalf("AuthMode = %q, want user", cfg.AuthMode)
	}
	if !cfg.ReadyForTelegram() {
		t.Fatal("QR login with API credentials should be ready to connect without a phone number")
	}
}

func TestWithoutTelegramAuthResetsLoginMethod(t *testing.T) {
	cfg := Config{AuthMode: AuthUser, LoginMethod: LoginQR, APIID: 1, APIHash: "hash"}

	got := cfg.WithoutTelegramAuth()

	if got.LoginMethod != LoginPhone {
		t.Fatalf("LoginMethod = %q, want the default after logout", got.LoginMethod)
	}
}

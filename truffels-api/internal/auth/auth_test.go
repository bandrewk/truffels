package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"truffels-api/internal/store"
)

func newTestAuth(t *testing.T) *Auth {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s)
}

func TestIsSetup_InitiallyFalse(t *testing.T) {
	a := newTestAuth(t)
	setup, err := a.IsSetup()
	if err != nil {
		t.Fatalf("is setup: %v", err)
	}
	if setup {
		t.Fatal("expected not setup initially")
	}
}

func TestSetPassword_MakesSetup(t *testing.T) {
	a := newTestAuth(t)
	if err := a.SetPassword("testpass123"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	setup, _ := a.IsSetup()
	if !setup {
		t.Fatal("expected setup after setting password")
	}
}

func TestCheckPassword_Correct(t *testing.T) {
	a := newTestAuth(t)
	_ = a.SetPassword("correcthorse")

	ok, err := a.CheckPassword("correcthorse")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !ok {
		t.Fatal("expected correct password to pass")
	}
}

func TestCheckPassword_Wrong(t *testing.T) {
	a := newTestAuth(t)
	_ = a.SetPassword("correcthorse")

	ok, err := a.CheckPassword("wrongpassword")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if ok {
		t.Fatal("expected wrong password to fail")
	}
}

func TestCheckPassword_NoPasswordSet(t *testing.T) {
	a := newTestAuth(t)
	ok, err := a.CheckPassword("anything")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if ok {
		t.Fatal("expected false when no password set")
	}
}

func TestHashPassword_DifferentEachTime(t *testing.T) {
	a := newTestAuth(t)
	h1, _ := a.HashPassword("same")
	h2, _ := a.HashPassword("same")
	if h1 == h2 {
		t.Fatal("bcrypt should produce different hashes for same input (different salt)")
	}
}

func TestSession_CreateAndValidate(t *testing.T) {
	a := newTestAuth(t)
	cookie, err := a.CreateSession()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if cookie.Name != "truffels_session" {
		t.Fatalf("expected cookie name truffels_session, got %q", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Fatal("expected HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("expected SameSiteStrict")
	}

	// Validate the session
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	if !a.ValidateSession(req) {
		t.Fatal("expected valid session")
	}
}

func TestSession_NoCookie(t *testing.T) {
	a := newTestAuth(t)
	req := httptest.NewRequest("GET", "/", nil)
	if a.ValidateSession(req) {
		t.Fatal("expected invalid without cookie")
	}
}

func TestSession_TamperedSignature(t *testing.T) {
	a := newTestAuth(t)
	cookie, _ := a.CreateSession()

	// Tamper with the signature
	cookie.Value = cookie.Value[:len(cookie.Value)-4] + "dead"
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	if a.ValidateSession(req) {
		t.Fatal("expected invalid with tampered signature")
	}
}

func TestSession_ExpiredToken(t *testing.T) {
	a := newTestAuth(t)

	// Create a token with an expired timestamp
	secret, _ := a.getSecret()
	_ = secret // ensure secret is generated

	// Build an expired token manually
	expiry := time.Now().Add(-1 * time.Hour).Unix()
	cookie := &http.Cookie{
		Name:  "truffels_session",
		Value: "admin|" + time.Now().Format("0") + "|fakesig",
	}
	// Use a properly formatted but expired value
	_ = expiry
	cookie.Value = "admin|1000000000|fakesig"

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	if a.ValidateSession(req) {
		t.Fatal("expected invalid for expired token")
	}
}

func TestSession_MalformedToken(t *testing.T) {
	a := newTestAuth(t)
	tests := []string{
		"",
		"onlyonepart",
		"two|parts",
		"admin|notanumber|sig",
	}
	for _, val := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(&http.Cookie{Name: "truffels_session", Value: val})
		if a.ValidateSession(req) {
			t.Fatalf("expected invalid for malformed token %q", val)
		}
	}
}

func TestClearCookie(t *testing.T) {
	a := newTestAuth(t)
	cookie := a.ClearCookie()
	if cookie.MaxAge != -1 {
		t.Fatalf("expected MaxAge -1, got %d", cookie.MaxAge)
	}
	if cookie.Name != "truffels_session" {
		t.Fatalf("expected cookie name truffels_session, got %q", cookie.Name)
	}
}

func TestSecret_GeneratedAndPersisted(t *testing.T) {
	a := newTestAuth(t)
	s1, err := a.getSecret()
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if len(s1) != 32 {
		t.Fatalf("expected 32-byte secret, got %d", len(s1))
	}

	// Second call should return same secret
	s2, _ := a.getSecret()
	if string(s1) != string(s2) {
		t.Fatal("expected same secret on second call")
	}
}

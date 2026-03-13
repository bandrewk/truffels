package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"truffels-api/internal/store"

	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName    = "truffels_session"
	sessionMaxAge = 86400 // 24 hours
	settingPwHash = "password_hash"
	settingSecret = "session_secret"
)

type Auth struct {
	store *store.Store
}

func New(st *store.Store) *Auth {
	return &Auth{store: st}
}

func (a *Auth) HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(h), err
}

func (a *Auth) CheckPassword(plain string) (bool, error) {
	hash, err := a.store.GetSetting(settingPwHash)
	if err != nil {
		return false, err
	}
	if hash == "" {
		return false, nil
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil, nil
}

func (a *Auth) IsSetup() (bool, error) {
	hash, err := a.store.GetSetting(settingPwHash)
	if err != nil {
		return false, err
	}
	return hash != "", nil
}

func (a *Auth) SetPassword(plain string) error {
	hash, err := a.HashPassword(plain)
	if err != nil {
		return err
	}
	return a.store.SetSetting(settingPwHash, hash)
}

func (a *Auth) getSecret() ([]byte, error) {
	s, err := a.store.GetSetting(settingSecret)
	if err != nil {
		return nil, err
	}
	if s != "" {
		return hex.DecodeString(s)
	}
	// Generate new secret
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := a.store.SetSetting(settingSecret, hex.EncodeToString(secret)); err != nil {
		return nil, err
	}
	return secret, nil
}

func (a *Auth) CreateSession() (*http.Cookie, error) {
	secret, err := a.getSecret()
	if err != nil {
		return nil, err
	}
	expiry := time.Now().Add(sessionMaxAge * time.Second).Unix()
	payload := fmt.Sprintf("admin|%d", expiry)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := payload + "|" + sig

	return &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}, nil
}

func (a *Auth) ValidateSession(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	parts := strings.SplitN(c.Value, "|", 3)
	if len(parts) != 3 {
		return false
	}
	// Check expiry
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return false
	}
	// Verify HMAC
	secret, err := a.getSecret()
	if err != nil {
		return false
	}
	payload := parts[0] + "|" + parts[1]
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[2]), []byte(expected))
}

func (a *Auth) ClearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

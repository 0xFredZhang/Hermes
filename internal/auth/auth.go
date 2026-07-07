package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

const sessionCookieName = "hermes_session"

// sessionPayload is the constant marker we sign. Since this is a single-user
// tool, a valid signature over this marker means "authenticated".
const sessionPayload = "authenticated"

type Authenticator struct {
	password string
	hmacKey  []byte
}

func New(password string, hmacKey []byte) *Authenticator {
	return &Authenticator{password: password, hmacKey: hmacKey}
}

func (a *Authenticator) CheckPassword(pw string) bool {
	return subtle.ConstantTimeCompare([]byte(pw), []byte(a.password)) == 1
}

func (a *Authenticator) sign(msg string) string {
	mac := hmac.New(sha256.New, a.hmacKey)
	mac.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Authenticator) IssueCookie() *http.Cookie {
	value := sessionPayload + "." + a.sign(sessionPayload)
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a *Authenticator) valid(cookieValue string) bool {
	parts := strings.SplitN(cookieValue, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expected := a.sign(parts[0])
	return parts[0] == sessionPayload &&
		hmac.Equal([]byte(expected), []byte(parts[1]))
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || r.URL.Path == "/healthz" ||
			strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(sessionCookieName)
		if err != nil || !a.valid(c.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

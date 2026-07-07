package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newAuth() *Authenticator { return New("hunter2", []byte("hmac-key-material")) }

func TestCheckPassword(t *testing.T) {
	a := newAuth()
	if !a.CheckPassword("hunter2") {
		t.Fatal("correct password rejected")
	}
	if a.CheckPassword("wrong") {
		t.Fatal("wrong password accepted")
	}
}

func TestMiddleware_RedirectsWhenUnauthenticated(t *testing.T) {
	a := newAuth()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/accounts", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login", loc)
	}
}

func TestMiddleware_AllowsWithValidCookie(t *testing.T) {
	a := newAuth()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req.AddCookie(a.IssueCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for authenticated request", rec.Code)
	}
}

func TestMiddleware_AllowsLoginAndStatic(t *testing.T) {
	a := newAuth()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, path := range []string{"/login", "/healthz", "/static/htmx.min.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("path %s: status = %d, want 200 (should bypass auth)", path, rec.Code)
		}
	}
}

func TestForgedCookieRejected(t *testing.T) {
	a := newAuth()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "forged.value"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("forged cookie accepted: status %d", rec.Code)
	}
}

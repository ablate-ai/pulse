package panel

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHandlerCSRF(t *testing.T) *Handler {
	t.Helper()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	return &Handler{csrfSecret: secret}
}

func TestCSRFTokenDeterministic(t *testing.T) {
	h := newTestHandlerCSRF(t)
	tok := h.csrfToken("session123")
	if tok == "" {
		t.Fatal("expected non-empty CSRF token")
	}
	if tok != h.csrfToken("session123") {
		t.Fatal("same session token should produce same CSRF token")
	}
}

func TestCSRFTokenDifferentSessions(t *testing.T) {
	h := newTestHandlerCSRF(t)
	if h.csrfToken("a") == h.csrfToken("b") {
		t.Fatal("different sessions should produce different CSRF tokens")
	}
}

func TestCSRFTokenDifferentSecrets(t *testing.T) {
	h1 := newTestHandlerCSRF(t)
	h2 := newTestHandlerCSRF(t)
	if h1.csrfToken("same") == h2.csrfToken("same") {
		t.Fatal("different secrets should produce different CSRF tokens")
	}
}

func TestValidateCSRF_Header(t *testing.T) {
	h := newTestHandlerCSRF(t)
	session := "mysession"
	csrf := h.csrfToken(session)

	req := httptest.NewRequest(http.MethodPost, "/panel/users", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: session})
	req.Header.Set("X-CSRF-Token", csrf)

	if !h.validateCSRF(req) {
		t.Fatal("valid CSRF token in header should pass")
	}
}

func TestValidateCSRF_FormField(t *testing.T) {
	h := newTestHandlerCSRF(t)
	session := "mysession"
	csrf := h.csrfToken(session)

	body := strings.NewReader("_csrf=" + csrf)
	req := httptest.NewRequest(http.MethodPost, "/logout", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: session})

	if !h.validateCSRF(req) {
		t.Fatal("valid CSRF token in form field should pass")
	}
}

func TestValidateCSRF_MissingToken(t *testing.T) {
	h := newTestHandlerCSRF(t)
	req := httptest.NewRequest(http.MethodPost, "/panel/users", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "mysession"})

	if h.validateCSRF(req) {
		t.Fatal("missing CSRF token should fail")
	}
}

func TestValidateCSRF_WrongToken(t *testing.T) {
	h := newTestHandlerCSRF(t)
	req := httptest.NewRequest(http.MethodPost, "/panel/users", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "mysession"})
	req.Header.Set("X-CSRF-Token", "wrong-token")

	if h.validateCSRF(req) {
		t.Fatal("wrong CSRF token should fail")
	}
}

func TestValidateCSRF_NoCookie(t *testing.T) {
	h := newTestHandlerCSRF(t)
	req := httptest.NewRequest(http.MethodPost, "/panel/users", nil)
	req.Header.Set("X-CSRF-Token", "anything")

	if h.validateCSRF(req) {
		t.Fatal("no session cookie should fail")
	}
}

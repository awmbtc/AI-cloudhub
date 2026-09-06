package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWizardHTMLRoutes(t *testing.T) {
	e := newJobSecurityEnv(t)
	for _, path := range []string{"/wizard", "/app"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		e.h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rr.Code, rr.Body.String())
		}
		ct := rr.Header().Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			t.Fatalf("%s Content-Type=%q", path, ct)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "连接向导") {
			t.Fatalf("%s missing Chinese title", path)
		}
		if !strings.Contains(body, "/v1/auth/login") && !strings.Contains(body, "auth/login") {
			t.Fatalf("%s missing login API reference in JS", path)
		}
	}

	// Trailing slash redirects to canonical
	req := httptest.NewRequest(http.MethodGet, "/wizard/", nil)
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("/wizard/ want 302 got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/wizard" {
		t.Fatalf("Location=%q", loc)
	}

	// Landing still HTML and links to wizard
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rr = httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/ status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "/wizard") {
		t.Fatal("landing should link to /wizard")
	}

	// Accept JSON on / still JSON
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	rr = httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/ json status=%d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("want json, got %s", rr.Header().Get("Content-Type"))
	}
	if !strings.Contains(rr.Body.String(), "wizard") {
		t.Fatal("JSON landing links should mention wizard")
	}
}

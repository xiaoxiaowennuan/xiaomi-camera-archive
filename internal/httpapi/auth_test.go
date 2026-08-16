package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mijia-archive/internal/auth"
	"mijia-archive/internal/media"
	"mijia-archive/internal/store"
)

func managementHandler(t *testing.T) (http.Handler, *store.Store, media.Library) {
	t.Helper()
	state, mount := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(mount, "initial", "MIJIA_RECORD_VIDEO"), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(filepath.Join(state, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	library := media.Library{HostRoot: "/srv/media", MountRoot: mount}
	initialMount, _ := library.IsArchiveFolder("/srv/media/initial")
	if _, err := database.EnsureBootstrapFolder(context.Background(), "Initial", "/srv/media/initial", initialMount); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("test-password-123!")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureAdmin(context.Background(), "admin", hash); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	handler := (&Server{Store: database, Auth: auth.New(database, false), Library: library, Location: loc}).Handler()
	return handler, database, library
}

func loginCookie(t *testing.T, handler http.Handler, username, password string) []*http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"`+username+`","password":"`+password+`"}`))
	req.Header.Set("Origin", "http://example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rr.Code, rr.Body.String())
	}
	return rr.Result().Cookies()
}

func authenticatedRequest(method, target, body string, cookies []*http.Cookie) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Origin", "http://example.com")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	return req
}

func TestAuthenticationAuthorizationAndFolderBoundary(t *testing.T) {
	handler, _, library := managementHandler(t)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/folders", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", rr.Code)
	}

	missingOrigin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"test-password-123!"}`))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, missingOrigin)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing origin status=%d", rr.Code)
	}

	adminCookies := loginCookie(t, handler, "admin", "test-password-123!")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authenticatedRequest(http.MethodPost, "/api/v1/users", `{"username":"viewer","password":"viewer-password-123!","passwordConfirmation":"viewer-password-123!","role":"user"}`, adminCookies))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", rr.Code, rr.Body.String())
	}

	viewerCookies := loginCookie(t, handler, "viewer", "viewer-password-123!")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authenticatedRequest(http.MethodGet, "/api/v1/users", "", viewerCookies))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("regular user management status=%d", rr.Code)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authenticatedRequest(http.MethodPost, "/api/v1/folders", `{"name":"Unsafe","serverPath":"/etc"}`, adminCookies))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unsafe folder status=%d", rr.Code)
	}

	if err := os.MkdirAll(filepath.Join(library.MountRoot, "second", "MIJIA_RECORD_VIDEO"), 0o700); err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authenticatedRequest(http.MethodPost, "/api/v1/folders", `{"name":"Second","serverPath":"/srv/media/second"}`, adminCookies))
	if rr.Code != http.StatusCreated {
		t.Fatalf("valid folder status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPasswordConfirmationAndChange(t *testing.T) {
	handler, _, _ := managementHandler(t)
	adminCookies := loginCookie(t, handler, "admin", "test-password-123!")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, authenticatedRequest(http.MethodPost, "/api/v1/users", `{"username":"mismatch","password":"new-password-123!","passwordConfirmation":"different-password-123!","role":"user"}`, adminCookies))
	if rr.Code != http.StatusBadRequest || !bytes.Contains(rr.Body.Bytes(), []byte("password_confirmation_mismatch")) {
		t.Fatalf("mismatched create status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authenticatedRequest(http.MethodPost, "/api/v1/auth/password", `{"currentPassword":"wrong-password","newPassword":"changed-password-123!","newPasswordConfirmation":"changed-password-123!"}`, adminCookies))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authenticatedRequest(http.MethodPost, "/api/v1/auth/password", `{"currentPassword":"test-password-123!","newPassword":"changed-password-123!","newPasswordConfirmation":"changed-password-123!"}`, adminCookies))
	if rr.Code != http.StatusOK {
		t.Fatalf("change password status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authenticatedRequest(http.MethodGet, "/api/v1/auth/me", "", adminCookies))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("old session status=%d body=%s", rr.Code, rr.Body.String())
	}
	_ = loginCookie(t, handler, "admin", "changed-password-123!")
}

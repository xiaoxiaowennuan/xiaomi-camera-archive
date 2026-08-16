package auth

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/alexedwards/scs/v2"
	"mijia-archive/internal/store"
)

var UsernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,31}$`)

const MinPasswordLength = 8

type Manager struct {
	Sessions *scs.SessionManager
	Store    *store.Store
}

func New(database *store.Store, secureCookies bool) *Manager {
	sessions := scs.New()
	sessions.Store = database
	sessions.Lifetime = 24 * time.Hour
	sessions.IdleTimeout = 2 * time.Hour
	sessions.Cookie.Name = "mijia_archive_session"
	sessions.Cookie.HttpOnly = true
	sessions.Cookie.Secure = secureCookies
	sessions.Cookie.SameSite = http.SameSiteStrictMode
	sessions.Cookie.Path = "/"
	return &Manager{Sessions: sessions, Store: database}
}

func ValidateNewUser(username, password, role string) error {
	if !UsernamePattern.MatchString(username) {
		return errors.New("invalid username")
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	if role != "admin" && role != "user" {
		return errors.New("invalid role")
	}
	return nil
}

func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength || len(password) > 128 {
		return errors.New("password must be between 8 and 128 characters")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	return argon2id.CreateHash(password, argon2id.DefaultParams)
}

func (m *Manager) Authenticate(ctx context.Context, username, password string) (store.UserRecord, bool) {
	if len(username) > 32 || len(password) > 128 {
		return store.UserRecord{}, false
	}
	user, err := m.Store.UserByUsername(ctx, strings.TrimSpace(username))
	if err != nil || !user.Active {
		// Keep the missing-user path expensive enough to reduce username timing signals.
		_, _ = argon2id.ComparePasswordAndHash(password, "$argon2id$v=19$m=65536,t=1,p=2$c29tZXNhbHRmb3J0aW1pbmc$EygvoITmqLQDYmVvF7YNaS8tB0S1uFdHqZGou+XUekI")
		return store.UserRecord{}, false
	}
	match, err := argon2id.ComparePasswordAndHash(password, user.PasswordHash)
	return user, err == nil && match
}

func (m *Manager) User(ctx context.Context) (store.UserRecord, error) {
	id := m.Sessions.GetInt64(ctx, "user_id")
	if id < 1 {
		return store.UserRecord{}, errors.New("unauthenticated")
	}
	user, err := m.Store.UserByID(ctx, id)
	if err != nil || !user.Active {
		return store.UserRecord{}, errors.New("unauthenticated")
	}
	return user, nil
}

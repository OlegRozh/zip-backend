package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"golang.org/x/crypto/bcrypt"
)

type fakeUserRepo struct {
	user *User
	err  error
}

func (f *fakeUserRepo) GetUserByEmailHash(_ context.Context, _ []byte) (*User, error) {
	return f.user, f.err
}

type fakeCache struct {
	called bool

	jti string
	rec cache.RefreshRecord
	ttl time.Duration

	err error
}

func (f *fakeCache) StoreRefresh(
	_ context.Context,
	jti string,
	rec cache.RefreshRecord,
	ttl time.Duration,
) error {
	f.called = true
	f.jti = jti
	f.rec = rec
	f.ttl = ttl

	return f.err
}

type fakeCrypto struct {
	hash []byte
}

func (f *fakeCrypto) Hash(_ []byte) []byte {
	return f.hash
}

func TestService_Login_Success(t *testing.T) {
	password := "correct-password"

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	repo := &fakeUserRepo{
		user: &User{
			ID:            "user-id",
			OrgID:         ptrString("org-id"),
			PasswordHash:  ptrString(string(passwordHash)),
			Role:          "defectologist",
			EmailVerified: true,
		},
	}
	cacheStore := &fakeCache{}
	crypto := &fakeCrypto{hash: []byte("email-hash")}

	svc := NewService(repo, cacheStore, testServiceConfig(), crypto)

	result, err := svc.Login(context.Background(), " USER@example.com ", password)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if result.AccessToken == "" {
		t.Fatal("access token is empty")
	}
	if result.RefreshToken == "" {
		t.Fatal("refresh token is empty")
	}
	if !cacheStore.called {
		t.Fatal("refresh token was not stored")
	}
	if cacheStore.rec.Status != "active" {
		t.Fatalf("refresh status = %q, want active", cacheStore.rec.Status)
	}
	if cacheStore.ttl != time.Hour {
		t.Fatalf("ttl = %v, want %v", cacheStore.ttl, time.Hour)
	}
}

func TestService_Login_WrongPassword(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	repo := &fakeUserRepo{
		user: &User{
			ID:            "user-id",
			OrgID:         ptrString("org-id"),
			PasswordHash:  ptrString(string(passwordHash)),
			Role:          "defectologist",
			EmailVerified: true,
		},
	}
	cacheStore := &fakeCache{}
	crypto := &fakeCrypto{hash: []byte("email-hash")}

	svc := NewService(repo, cacheStore, testServiceConfig(), crypto)

	_, err = svc.Login(context.Background(), "user@example.com", "wrong-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want %v", err, ErrInvalidCredentials)
	}
	if cacheStore.called {
		t.Fatal("refresh token should not be stored")
	}
}

func TestService_Login_UserNotFound(t *testing.T) {
	repo := &fakeUserRepo{
		err: ErrUserNotFound,
	}
	cacheStore := &fakeCache{}
	crypto := &fakeCrypto{hash: []byte("email-hash")}

	svc := NewService(repo, cacheStore, testServiceConfig(), crypto)

	_, err := svc.Login(context.Background(), "missing@example.com", "password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want %v", err, ErrInvalidCredentials)
	}
	if cacheStore.called {
		t.Fatal("refresh token should not be stored")
	}
}

func TestService_Login_EmailNotVerified(t *testing.T) {
	password := "correct-password"

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	repo := &fakeUserRepo{
		user: &User{
			ID:            "user-id",
			OrgID:         ptrString("org-id"),
			PasswordHash:  ptrString(string(passwordHash)),
			Role:          "defectologist",
			EmailVerified: false,
		},
	}
	cacheStore := &fakeCache{}
	crypto := &fakeCrypto{hash: []byte("email-hash")}

	cfg := testServiceConfig()
	cfg.RequireEmailVerification = true

	svc := NewService(repo, cacheStore, cfg, crypto)

	_, err = svc.Login(context.Background(), "user@example.com", password)
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("err = %v, want %v", err, ErrEmailNotVerified)
	}
	if cacheStore.called {
		t.Fatal("refresh token should not be stored")
	}
}

func testServiceConfig() *ServiceConfig {
	return &ServiceConfig{
		JWTSecret:       "01234567890123456789012345678901",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
	}
}

func ptrString(s string) *string {
	return &s
}

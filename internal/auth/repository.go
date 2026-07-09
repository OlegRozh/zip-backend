package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUserNotFound = errors.New("user not found")

type User struct {
	ID            string
	OrgID         *string
	PasswordHash  *string
	Role          string
	EmailVerified bool
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{
		pool: pool,
	}
}

func (r *Repository) GetUserByEmailHash(ctx context.Context, emailHash []byte) (*User, error) {
	var user User
	query := `
	SELECT users.id, users.org_id, auth_cred.password_hash, auth_cred.role, users.email_verified
	FROM users
	JOIN auth_cred ON auth_cred.user_id = users.id
	WHERE auth_cred.email_hash = $1
	`
	row := r.pool.QueryRow(ctx, query, emailHash)
	err := row.Scan(&user.ID, &user.OrgID, &user.PasswordHash, &user.Role, &user.EmailVerified)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email hash: %w", err)
	}
	return &user, nil
}

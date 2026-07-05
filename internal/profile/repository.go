package profile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUserNotFound = errors.New("user not found")

type ObjectSizeFunc func(ctx context.Context, key string) (int64, error)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

type UserAvatar struct {
	OrgID     sql.NullString
	AvatarKey sql.NullString
}

type AvatarChange struct {
	OldKey  string
	OldSize int64
	OrgID   sql.NullString
}

func (r *Repository) ReplaceAvatar(
	ctx context.Context,
	userID string,
	newKey string,
	newSize int64,
	objectSize ObjectSizeFunc,
) (AvatarChange, error) {
	return r.changeAvatar(ctx, userID, &newKey, newSize, objectSize)
}

func (r *Repository) ClearAvatar(
	ctx context.Context,
	userID string,
	objectSize ObjectSizeFunc,
) (AvatarChange, error) {
	return r.changeAvatar(ctx, userID, nil, 0, objectSize)
}

func (r *Repository) RestoreAvatarIfEmpty(ctx context.Context, userID string, oldKey string, oldSize int64) (bool, error) {
	if oldKey == "" {
		return false, nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin restore avatar tx: %w", err)
	}
	defer rollbackAvatarTx(ctx, tx)

	current, err := lockUserAvatar(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	if current.AvatarKey.Valid {
		return false, nil
	}

	if err := updateUserAvatar(ctx, tx, userID, &oldKey); err != nil {
		return false, err
	}
	if current.OrgID.Valid && oldSize != 0 {
		if err := updateOrgStorageUsage(ctx, tx, current.OrgID.String, oldSize); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit restore avatar tx: %w", err)
	}

	return true, nil
}

func (r *Repository) CurrentAvatarKey(ctx context.Context, userID string) (string, error) {
	var avatarKey sql.NullString
	err := r.db.QueryRow(ctx, `
		SELECT avatar_key
		FROM users
		WHERE id = $1
	`, userID).Scan(&avatarKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read current avatar key: %w", err)
	}
	return nullStringValue(avatarKey), nil
}

func (r *Repository) AddOrgStorageUsage(ctx context.Context, orgID string, delta int64) error {
	if orgID == "" || delta == 0 {
		return nil
	}

	_, err := r.db.Exec(ctx, `
		UPDATE organizations
		SET storage_used_bytes = GREATEST(storage_used_bytes + $2::bigint, 0::bigint)
		WHERE id = $1
	`, orgID, delta)
	if err != nil {
		return fmt.Errorf("compensate organization storage usage: %w", err)
	}
	return nil
}

func (r *Repository) changeAvatar(
	ctx context.Context,
	userID string,
	newKey *string,
	newSize int64,
	objectSize ObjectSizeFunc,
) (AvatarChange, error) {
	if objectSize == nil {
		return AvatarChange{}, errors.New("object size function is required")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return AvatarChange{}, fmt.Errorf("begin avatar tx: %w", err)
	}
	defer rollbackAvatarTx(ctx, tx)

	current, err := lockUserAvatar(ctx, tx, userID)
	if err != nil {
		return AvatarChange{}, err
	}

	oldKey := nullStringValue(current.AvatarKey)
	oldSize, err := currentObjectSize(ctx, oldKey, objectSize)
	if err != nil {
		return AvatarChange{}, err
	}

	storageDelta := -oldSize
	if newKey != nil {
		storageDelta = newSize - oldSize
	}

	if err := updateUserAvatar(ctx, tx, userID, newKey); err != nil {
		return AvatarChange{}, err
	}
	if current.OrgID.Valid && storageDelta != 0 {
		if err := updateOrgStorageUsage(ctx, tx, current.OrgID.String, storageDelta); err != nil {
			return AvatarChange{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AvatarChange{}, fmt.Errorf("commit avatar tx: %w", err)
	}

	return AvatarChange{
		OldKey:  oldKey,
		OldSize: oldSize,
		OrgID:   current.OrgID,
	}, nil
}

func rollbackAvatarTx(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}

func lockUserAvatar(ctx context.Context, tx pgx.Tx, userID string) (UserAvatar, error) {
	var avatar UserAvatar
	err := tx.QueryRow(ctx, `
		SELECT org_id::text, avatar_key
		FROM users
		WHERE id = $1
		FOR UPDATE
	`, userID).Scan(&avatar.OrgID, &avatar.AvatarKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserAvatar{}, ErrUserNotFound
	}
	if err != nil {
		return UserAvatar{}, fmt.Errorf("lock user for avatar update: %w", err)
	}
	return avatar, nil
}

func currentObjectSize(ctx context.Context, key string, objectSize ObjectSizeFunc) (int64, error) {
	var size int64
	if key != "" {
		objectSizeValue, err := objectSize(ctx, key)
		if err != nil {
			return 0, fmt.Errorf("get current avatar object size: %w", err)
		}
		size = objectSizeValue
	}
	return size, nil
}

func updateUserAvatar(ctx context.Context, tx pgx.Tx, userID string, newKey *string) error {
	var avatarKey any
	if newKey != nil {
		avatarKey = *newKey
	}

	_, err := tx.Exec(ctx, `
		UPDATE users
		SET avatar_key = $2, updated_at = now()
		WHERE id = $1
	`, userID, avatarKey)
	if err != nil {
		return fmt.Errorf("update user avatar key: %w", err)
	}
	return nil
}

func updateOrgStorageUsage(ctx context.Context, tx pgx.Tx, orgID string, delta int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE organizations
		SET storage_used_bytes = GREATEST(storage_used_bytes + $2::bigint, 0::bigint)
		WHERE id = $1
	`, orgID, delta)
	if err != nil {
		return fmt.Errorf("update organization storage usage: %w", err)
	}
	return nil
}

func nullStringValue(value sql.NullString) string {
	result := ""
	if value.Valid {
		result = value.String
	}
	return result
}

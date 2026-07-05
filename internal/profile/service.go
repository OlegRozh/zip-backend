package profile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
)

const avatarURLTTL = 15 * time.Minute

type ObjectStorage interface {
	PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	RemoveObject(ctx context.Context, key string) error
	ObjectSize(ctx context.Context, key string) (int64, error)
	PresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type AvatarRepository interface {
	ReplaceAvatar(ctx context.Context, userID string, newKey string, newSize int64, objectSize ObjectSizeFunc) (AvatarChange, error)
	ClearAvatar(ctx context.Context, userID string, objectSize ObjectSizeFunc) (AvatarChange, error)
	RestoreAvatarIfEmpty(ctx context.Context, userID string, oldKey string, oldSize int64) (bool, error)
	AddOrgStorageUsage(ctx context.Context, orgID string, delta int64) error
	CurrentAvatarKey(ctx context.Context, userID string) (string, error)
}

type Service struct {
	repo    AvatarRepository
	storage ObjectStorage
}

func NewService(repo AvatarRepository, storageClient ObjectStorage) *Service {
	return &Service{repo: repo, storage: storageClient}
}

func (s *Service) ReplaceAvatar(ctx context.Context, userID string, reader io.Reader, size int64, mimeType string) (string, error) {
	newKey := avatarKey(userID)
	if err := s.storage.PutObject(ctx, newKey, reader, size, mimeType); err != nil {
		return "", fmt.Errorf("put avatar object: %w", err)
	}

	avatarURL, err := s.storage.PresignedURL(ctx, newKey, avatarURLTTL)
	if err != nil {
		s.cleanupNewObject(ctx, newKey)
		return "", fmt.Errorf("generate avatar url: %w", err)
	}

	change, err := s.repo.ReplaceAvatar(ctx, userID, newKey, size, s.objectSize)
	if err != nil {
		s.cleanupNewObject(ctx, newKey)
		return "", profileError(err)
	}

	if err := s.removeObject(ctx, change.OldKey); err != nil {
		s.compensateOldObjectUsage(ctx, change, err)
	}

	return s.currentAvatarURL(ctx, userID, newKey, avatarURL), nil
}

func (s *Service) DeleteAvatar(ctx context.Context, userID string) error {
	change, err := s.repo.ClearAvatar(ctx, userID, s.objectSize)
	if err != nil {
		return profileError(err)
	}

	if err := s.removeObject(ctx, change.OldKey); err != nil {
		s.restoreDeletedAvatar(ctx, userID, change, err)
		return err
	}
	return nil
}

func (s *Service) currentAvatarURL(ctx context.Context, userID string, expectedKey string, fallbackURL string) string {
	currentKey, err := s.repo.CurrentAvatarKey(ctx, userID)
	if err != nil {
		slog.Warn("read current avatar key before response failed", "user_id", userID, "err", err)
		return fallbackURL
	}
	if currentKey == "" || currentKey == expectedKey {
		return fallbackURL
	}

	currentURL, err := s.storage.PresignedURL(ctx, currentKey, avatarURLTTL)
	if err != nil {
		slog.Warn("generate current avatar url before response failed", "key", currentKey, "err", err)
		return fallbackURL
	}
	return currentURL
}

func (s *Service) objectSize(ctx context.Context, key string) (int64, error) {
	var size int64
	if key != "" {
		objectSize, err := s.storage.ObjectSize(ctx, key)
		if errors.Is(err, storage.ErrObjectNotFound) {
			size = 0
		} else if err != nil {
			return 0, fmt.Errorf("stat avatar object: %w", err)
		} else {
			size = objectSize
		}
	}
	return size, nil
}

func (s *Service) removeObject(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	if err := s.storage.RemoveObject(ctx, key); err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
		return fmt.Errorf("remove avatar object: %w", err)
	}
	return nil
}

func (s *Service) cleanupNewObject(ctx context.Context, key string) {
	if err := s.removeObject(ctx, key); err != nil {
		slog.Error("cleanup uploaded avatar after db error failed", "key", key, "err", err)
	}
}

func (s *Service) compensateOldObjectUsage(ctx context.Context, change AvatarChange, cause error) {
	slog.Error("old avatar object cleanup failed", "key", change.OldKey, "err", cause)
	if !change.OrgID.Valid || change.OldSize == 0 {
		return
	}
	if err := s.repo.AddOrgStorageUsage(ctx, change.OrgID.String, change.OldSize); err != nil {
		slog.Error("old avatar storage usage compensation failed",
			"key", change.OldKey,
			"old_size", change.OldSize,
			"err", err,
		)
	}
}

func (s *Service) restoreDeletedAvatar(ctx context.Context, userID string, change AvatarChange, cause error) {
	slog.Error("avatar object delete failed", "key", change.OldKey, "err", cause)
	restored, err := s.repo.RestoreAvatarIfEmpty(ctx, userID, change.OldKey, change.OldSize)
	if err != nil {
		slog.Error("restore avatar after delete failure failed", "key", change.OldKey, "err", err)
		s.compensateOldObjectUsage(ctx, change, cause)
		return
	}
	if !restored {
		s.compensateOldObjectUsage(ctx, change, cause)
	}
}

func profileError(err error) error {
	if errors.Is(err, ErrUserNotFound) {
		return apperr.ErrUnauthorized
	}
	return err
}

func avatarKey(userID string) string {
	return fmt.Sprintf("avatars/%s/%s", userID, uuid.New().String())
}

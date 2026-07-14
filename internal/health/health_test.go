package health

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error {
	return f(ctx)
}

type connectionCheckerFunc func() bool

func (f connectionCheckerFunc) IsConnected() bool {
	return f()
}

type listerFunc func(context.Context) ([]minio.BucketInfo, error)

func (f listerFunc) ListBuckets(ctx context.Context) ([]minio.BucketInfo, error) {
	return f(ctx)
}

func TestCheckerRunAllDependenciesReady(t *testing.T) {
	checker := newTestChecker(nil, nil, nil)

	status, body := checker.Run(context.Background())
	result := body.(response)

	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ok", result.Status)
	require.Len(t, result.Checks, 4)
	for _, check := range result.Checks {
		assert.Equal(t, "ok", check.Status)
		assert.Empty(t, check.Error)
	}
}

func TestCheckerRunDependencyError(t *testing.T) {
	checker := newTestChecker(errors.New("connection refused"), nil, nil)

	status, body := checker.Run(context.Background())
	result := body.(response)

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "degraded", result.Status)
	assert.Equal(t, "error", result.Checks["redis"].Status)
	assert.Equal(t, "connection refused", result.Checks["redis"].Error)
	assert.Equal(t, "ok", result.Checks["postgres"].Status)
	assert.Equal(t, "ok", result.Checks["nats"].Status)
	assert.Equal(t, "ok", result.Checks["minio"].Status)
}

func TestCheckerRunRecoversCheckPanic(t *testing.T) {
	checker := newTestChecker(nil, nil, func(context.Context) ([]minio.BucketInfo, error) {
		panic("minio exploded")
	})

	status, body := checker.Run(context.Background())
	result := body.(response)

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "degraded", result.Status)
	assert.Equal(t, "error", result.Checks["minio"].Status)
	assert.True(t, strings.Contains(result.Checks["minio"].Error, "panic: minio exploded"))
}

func TestCheckerRunTimesOutChecksInParallel(t *testing.T) {
	waitForContext := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	checker := &Checker{
		DB:          pingerFunc(waitForContext),
		RedisClient: pingerFunc(waitForContext),
		NatsConn:    connectionCheckerFunc(func() bool { return true }),
		MinioClient: listerFunc(func(ctx context.Context) ([]minio.BucketInfo, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	}

	startedAt := time.Now()
	status, body := checker.Run(context.Background())
	elapsed := time.Since(startedAt)
	result := body.(response)

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "degraded", result.Status)
	assert.Less(t, elapsed, 3*time.Second)
	assert.GreaterOrEqual(t, elapsed, checkTimeout)
	assert.Equal(t, context.DeadlineExceeded.Error(), result.Checks["postgres"].Error)
	assert.Equal(t, context.DeadlineExceeded.Error(), result.Checks["redis"].Error)
	assert.Equal(t, context.DeadlineExceeded.Error(), result.Checks["minio"].Error)
}

func newTestChecker(
	redisErr error,
	postgresErr error,
	minioCheck listerFunc,
) *Checker {
	if minioCheck == nil {
		minioCheck = func(context.Context) ([]minio.BucketInfo, error) {
			return nil, nil
		}
	}

	return &Checker{
		DB:          pingerFunc(func(context.Context) error { return postgresErr }),
		RedisClient: pingerFunc(func(context.Context) error { return redisErr }),
		NatsConn:    connectionCheckerFunc(func() bool { return true }),
		MinioClient: minioCheck,
	}
}

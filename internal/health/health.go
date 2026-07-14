package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"golang.org/x/sync/errgroup"
)

const checkTimeout = 2 * time.Second

// Pinger — интерфейс для проверки зависимости через Ping.
type Pinger interface {
	Ping(ctx context.Context) error
}

// ConnectionChecker — интерфейс для проверки состояния подключения.
type ConnectionChecker interface {
	IsConnected() bool
}

// Lister — интерфейс для проверки MinIO.
type Lister interface {
	ListBuckets(ctx context.Context) ([]minio.BucketInfo, error)
}

type checkResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type response struct {
	Status string                 `json:"status"`
	Checks map[string]checkResult `json:"checks"`
}

// Checker содержит клиенты для проверки зависимостей.
type Checker struct {
	DB          Pinger
	RedisClient Pinger
	NatsConn    ConnectionChecker
	MinioClient Lister
}

// Run запускает параллельные проверки с таймаутом 2 секунды на каждую проверку.
// Возвращает HTTP статус и тело ответа.
func (c *Checker) Run(ctx context.Context) (int, interface{}) {
	checks := c.checks()
	results := make(map[string]checkResult, len(checks))
	var group errgroup.Group
	var mu sync.Mutex

	setResult := func(name string, err error) {
		result := checkResult{Status: "ok"}
		if err != nil {
			result = checkResult{Status: "error", Error: err.Error()}
		}
		mu.Lock()
		results[name] = result
		mu.Unlock()
	}

	for name, check := range checks {
		if err := ctx.Err(); err != nil {
			setResult(name, err)
		} else {
			group.Go(func() error {
				err := runCheck(ctx, check)
				setResult(name, err)
				return err
			})
		}
	}

	waitErr := group.Wait()
	status := "ok"
	httpStatus := http.StatusOK
	if waitErr != nil || hasErrors(results) {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}

	return httpStatus, response{
		Status: status,
		Checks: results,
	}
}

func (c *Checker) checks() map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"postgres": c.DB.Ping,
		"redis":    c.RedisClient.Ping,
		"nats": func(context.Context) error {
			if !c.NatsConn.IsConnected() {
				return errors.New("nats connection is closed")
			}
			return nil
		},
		"minio": func(ctx context.Context) error {
			_, err := c.MinioClient.ListBuckets(ctx)
			return err
		},
	}
}

func runCheck(ctx context.Context, check func(context.Context) error) (err error) {
	checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()

	return check(checkCtx)
}

func hasErrors(results map[string]checkResult) bool {
	hasError := false
	for _, result := range results {
		if result.Status == "error" {
			hasError = true
		}
	}
	return hasError
}

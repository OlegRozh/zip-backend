package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/nats-io/nats.go"
	"golang.org/x/sync/errgroup"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
)

// Pinger — интерфейс для проверки PostgreSQL.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Lister — интерфейс для проверки MinIO.
type Lister interface {
	ListBuckets(ctx context.Context) ([]minio.BucketInfo, error)
}

type checkResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Checker содержит клиенты для проверки зависимостей.
type Checker struct {
	DB          Pinger
	RedisClient *cache.Client
	NatsConn    *nats.Conn
	MinioClient Lister
}

// Run запускает параллельные проверки с таймаутом 2 секунды.
// Возвращает HTTP статус и тело ответа.
func (c *Checker) Run(ctx context.Context) (int, interface{}) {
	// Контекст с таймаутом для всех проверок
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	g, ctxGroup := errgroup.WithContext(checkCtx)
	var mu sync.Mutex
	results := make(map[string]checkResult)

	// Определяем проверки для каждой зависимости.
	checks := map[string]func(context.Context) error{
		"postgres": func(ctx context.Context) error {
			return c.DB.Ping(ctx)
		},
		"redis": func(ctx context.Context) error {
			return c.RedisClient.Ping(ctx)
		},
		"nats": func(ctx context.Context) error {
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

	// Запускаем проверки параллельно.
	// Для Go 1.22+ не требуется явное присваивание переменных цикла, но оставим для ясности.
	for name, check := range checks {
		if ctxGroup.Err() != nil {
			break
		}
		name, check := name, check // захват переменных для горутины для старых версий Go.
		g.Go(func() (err error) {
			// Защита от паники внутри проверки.
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					defer mu.Unlock()
					results[name] = checkResult{
						Status: "error",
						Error:  fmt.Sprintf("panic: %v", r),
					}
					err = nil
				}
			}()
			if err := check(ctxGroup); err != nil {
				mu.Lock()
				defer mu.Unlock()
				results[name] = checkResult{Status: "error", Error: err.Error()}
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			results[name] = checkResult{Status: "ok"}
			return nil
		})
	}

	// Ожидаем завершения всех горутин.
	// обрабатываем её на случай будущих изменений.
	if err := g.Wait(); err != nil {
		slog.Error("health: errgroup wait failed", "err", err)
	}

	finalStatus := "ok"
	for _, res := range results {
		if res.Status == "error" {
			finalStatus = "degraded"
			break
		}
	}

	httpStatus := http.StatusOK
	if finalStatus == "degraded" {
		httpStatus = http.StatusServiceUnavailable
	}

	response := struct {
		Status string                 `json:"status"`
		Checks map[string]checkResult `json:"checks"`
	}{
		Status: finalStatus,
		Checks: results,
	}
	return httpStatus, response
}

// Package apperr предоставляет единый формат ошибок API и базовые константы ошибок.
package apperr

import "fmt"

// AppError представляет стандартизированную ошибку приложения для API.
type AppError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
	Err        error  `json:"-"`
}

var (
	// ErrNotFound возвращается, когда запрашиваемый ресурс не найден.
	ErrNotFound = &AppError{Code: "NOT_FOUND", HTTPStatus: 404, Message: "resource not found"}

	// ErrUnauthorized возвращается, когда пользователь не авторизован.
	ErrUnauthorized = &AppError{Code: "UNAUTHORIZED", HTTPStatus: 401, Message: "unauthorized"}

	// ErrForbidden возвращается, когда у пользователя нет прав на выполнение операции.
	ErrForbidden = &AppError{Code: "FORBIDDEN", HTTPStatus: 403, Message: "forbidden"}

	// ErrBadRequest возвращается при некорректных входных данных в запросе.
	ErrBadRequest = &AppError{Code: "BAD_REQUEST", HTTPStatus: 400, Message: "bad request"}

	// ErrConflict возвращается, когда операция вызывает конфликт состояний (например, дубликат email).
	ErrConflict = &AppError{Code: "CONFLICT", HTTPStatus: 409, Message: "conflict"}

	// ErrInternal возвращается при непредвиденных системных сбоях на стороне сервера.
	ErrInternal = &AppError{Code: "INTERNAL", HTTPStatus: 500, Message: "internal server error"}
)

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// WithMessage возвращает копию AppError с измененным сообщением.
func (e *AppError) WithMessage(msg string) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    msg,
		HTTPStatus: e.HTTPStatus,
		Err:        e.Err,
	}
}

// WithError оборачивает исходную Go-ошибку внутрь AppError.
func (e *AppError) WithError(err error) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    e.Message,
		HTTPStatus: e.HTTPStatus,
		Err:        err,
	}
}

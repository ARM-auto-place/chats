package errors

import (
	"errors"
	"fmt"

	"github.com/valyala/fasthttp"
)

const (
	noneValue = "None"
	Errors    = "errors"
)

type Error struct {
	ErrorText  string
	Cause      map[string]interface{}
	statusCode int
	trKey      string

	internalError error
}

func (e *Error) Error() string {
	var cause string
	if e.Cause != nil {
		cause = fmt.Sprintf(". Causes: %v", e.Cause)
	}
	return e.ErrorText + cause
}

func New(msg string, statusCode int, outerError string) *Error {
	return &Error{ErrorText: msg, statusCode: statusCode, trKey: outerError}
}

var (
	ForbiddenError      = func() *Error { return New("forbidden", fasthttp.StatusForbidden, ErrForbidden) }
	UnauthorizedError   = func() *Error { return New("unauthorized", fasthttp.StatusUnauthorized, ErrUnauthorized) }
	BadRequestError     = func() *Error { return New("bad request", fasthttp.StatusBadRequest, ErrBadRequest) }
	InternalServerError = func() *Error {
		return New("internal server error", fasthttp.StatusInternalServerError, ErrInternal)
	}
	NotFoundError = func() *Error { return New("not found", fasthttp.StatusNotFound, ErrNotFound) }
)

const (
	ErrInternal     = "chat.errors.internalError"
	ErrBadRequest   = "chat.errors.badRequest"
	ErrForbidden    = "chat.errors.forbidden"
	ErrUnauthorized = "chat.errors.unauthorized"
	ErrNotFound     = "chat.errors.notFound"
)

func (e *Error) SetStatusCode(code int) *Error  { e.statusCode = code; return e }
func (e *Error) GetStatusCode() int             { return e.statusCode }
func (e *Error) SetOuterError(err error) *Error { e.internalError = err; return e }
func (e *Error) GetOuterError() error           { return e.internalError }
func (e *Error) GetTranslationKey() string      { return e.trKey }
func (e *Error) Unwrap() error                  { return e.internalError }

func (e *Error) AddCause(args ...string) *Error {
	if e.Cause == nil {
		e.Cause = make(map[string]interface{})
	}
	for i := 0; i < len(args); i += 2 {
		strKey := args[i]
		e.Cause[strKey] = noneValue
		if i+1 < len(args) {
			e.Cause[strKey] = args[i+1]
		}
	}
	return e
}

// Code returns the HTTP status code. The generated transport layer
// (jsonRPC/externalapi) type-asserts errors against a `Code() int` method to
// set the response status, so this must stay in sync with statusCode.
func (e *Error) Code() int { return e.statusCode }

func Is(errOne, errTwo error) bool {
	var e1, e2 *Error
	if errors.As(errOne, &e1) && errors.As(errTwo, &e2) {
		return e1.ErrorText == e2.ErrorText
	}
	return errOne.Error() == errTwo.Error()
}

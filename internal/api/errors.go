package api

import (
	"errors"
	"log"
	"net/http"

	"deploy/internal/types"
)

// ErrorBody creates the appropriate ErrorResponse based on error type.
func ErrorBody(err error) types.ErrorResponse {
	switch e := err.(type) {
	case *types.ValidationError:
		return types.ErrorResponse{
			Error:  e.Message,
			Code:   string(e.Code),
			Detail: e.Detail,
		}
	case *types.BuildError:
		return types.ErrorResponse{
			Error:  e.Message,
			Code:   string(e.Code),
			Detail: e.Detail, // Docker output — useful for debugging
		}
	case *types.InfraError:
		return types.ErrorResponse{
			Error:  string(e.Code),
			Code:   string(e.Code),
			Detail: e.Message,
		}
	case *types.ConfigError:
		return types.ErrorResponse{
			Error:  e.Message,
			Code:   string(e.Code),
			Detail: e.Field,
		}
	case *types.SystemError:
		log.Printf("SYSTEM ERROR: %v", e.Err) // Log the real error
		return types.ErrorResponse{
			Error:  "internal system error",
			Code:   string(e.Code),
			Detail: "contact support with error code: " + string(e.Code),
		}
	default:
		// Unknown error type — treat as system error
		log.Printf("UNKNOWN ERROR: %v", err)
		return types.ErrorResponse{
			Error:  "internal server error",
			Code:   string(types.ErrInternal),
		}
	}
}

// statusCodeForError maps error types to HTTP status codes.
func statusCodeForError(err error) int {
	switch err.(type) {
	case *types.ValidationError:
		return http.StatusBadRequest
	case *types.BuildError:
		return http.StatusInternalServerError
	case *types.InfraError:
		return http.StatusServiceUnavailable
	case *types.ConfigError:
		return http.StatusInternalServerError
	case *types.SystemError:
		return http.StatusInternalServerError
	default:
		if errors.Is(err, types.ErrNotFoundSentinel) {
			return http.StatusNotFound
		}
		return http.StatusInternalServerError
	}
}

// Helper constructors that return ErrorResponse directly.

// NotFoundError creates a 404 error body.
func NotFoundError(entity string) types.ErrorResponse {
	return types.ErrorResponse{
		Error:  entity + " not found",
		Code:   string(types.ErrNotFound),
	}
}

// BadRequestError creates a 400 error body.
func BadRequestError(detail string) types.ErrorResponse {
	return types.ErrorResponse{
		Error:  "bad request",
		Code:   string(types.ErrBadRequest),
		Detail: detail,
	}
}

// ConflictError creates a 409 error body.
func ConflictError(detail string) types.ErrorResponse {
	return types.ErrorResponse{
		Error:  "conflict",
		Code:   string(types.ErrConflict),
		Detail: detail,
	}
}

// Helper error constructors (return error for use with ErrorBody).

// systemError wraps an internal error as a SystemError.
func systemError(err error) error {
	return &types.SystemError{Code: types.ErrInternal, Err: err}
}

// notFoundAsError returns a not-found error.
func notFoundAsError(entity string) error {
	return &types.SystemError{Code: types.ErrNotFound, Message: entity + " not found"}
}

// appRunningError returns an app-running conflict error.
func appRunningError(msg string) error {
	return &types.SystemError{Code: types.ErrAppRunning, Message: msg}
}

// appNotRunningError returns an app-not-running error.
func appNotRunningError(msg string) error {
	return &types.SystemError{Code: types.ErrAppNotRunning, Message: msg}
}

// dockerError wraps a Docker-related error as an InfraError.
func dockerError(detail string) error {
	return &types.InfraError{Code: types.ErrDocker, Message: detail}
}

package api

import (
	"log"

	"deploy/internal/types"
)

// sanitizeError logs the full error for server-side debugging and returns a
// generic message safe for client-facing API responses.
func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	log.Printf("error: %v", err)
	return "internal server error"
}

// ErrorBody creates a types.ErrorResponse, sanitizing the detail for internal
// and Docker error codes to avoid leaking filesystem paths or other sensitive
// information to API clients.
func ErrorBody(code string, detail string) types.ErrorResponse {
	if code == types.ErrInternal || code == types.ErrDocker || code == "BACKUP_FAILED" {
		log.Printf("API error [%s]: %s", code, detail)
		detail = "internal server error"
	}
	return types.ErrorResponse{
		Error:  code,
		Code:   code,
		Detail: detail,
	}
}

// NotFoundError creates a 404 error body.
func NotFoundError(entity string) types.ErrorResponse {
	return ErrorBody(types.ErrNotFound, entity+" not found")
}

// BadRequestError creates a 400 error body.
func BadRequestError(detail string) types.ErrorResponse {
	return ErrorBody(types.ErrBadRequest, detail)
}

// ConflictError creates a 409 error body.
func ConflictError(detail string) types.ErrorResponse {
	return ErrorBody(types.ErrConflict, detail)
}

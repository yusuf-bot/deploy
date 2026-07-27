package api

import "deploy/internal/types"

// ErrorBody creates a types.ErrorResponse.
func ErrorBody(code string, detail string) types.ErrorResponse {
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

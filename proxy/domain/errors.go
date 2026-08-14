package domain

import "errors"

// Sentinel domain errors. Handlers map these to HTTP status codes.
var (
	// ErrNotFound is returned when a requested resource does not exist.
	ErrNotFound = errors.New("resource not found")
	// ErrConflict is returned when the requested action conflicts with state.
	ErrConflict = errors.New("conflict with current state")
	// ErrBadParamInput is returned for invalid request parameters.
	ErrBadParamInput        = errors.New("invalid request parameter")
	ErrInvalidSource        = errors.New("input must contain exactly one source")
	ErrInvalidBase64        = errors.New("invalid base64 input")
	ErrBase64TooLarge       = errors.New("base64 input exceeds the decoded size limit")
	ErrURLContentTooLarge   = errors.New("URL content exceeds the download size limit")
	ErrUnsupportedMediaType = errors.New("unsupported media type")
	ErrFileValidation       = errors.New("file validation failed")
	ErrInvalidURL           = errors.New("invalid or unreachable input URL")
	ErrSSRFBlocked          = errors.New("input URL resolves to a blocked address")
	ErrResultExpired        = errors.New("OCR result has expired")
	// ErrStorageUnavailable is returned when a backing store is unreachable.
	ErrStorageUnavailable = errors.New("storage unavailable")
	// ErrUnauthorized is returned when credentials are missing/invalid/revoked.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrRateLimited is returned when a request exceeds the key's or user's rate limit.
	ErrRateLimited = errors.New("rate limited")
	// ErrQuotaExceeded is returned when a request exceeds the user's doc quota.
	ErrQuotaExceeded = errors.New("quota exceeded")
	// ErrStorageQuotaExceeded is returned when retained and reserved input bytes
	// would exceed the account's aggregate storage quota.
	ErrStorageQuotaExceeded = errors.New("storage byte quota exceeded")
	// ErrUserDisabled is returned when the account has been disabled by an admin.
	ErrUserDisabled = errors.New("user account is disabled")
)

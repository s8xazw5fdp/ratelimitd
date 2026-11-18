package ratelimitd

import "errors"

var (
	// ErrInvalidRate is returned when Params.Rate is not positive.
	ErrInvalidRate = errors.New("ratelimitd: rate must be positive")
	// ErrInvalidBurst is returned when Params.Burst is below one token.
	ErrInvalidBurst = errors.New("ratelimitd: burst must be at least 1")
	// ErrNoTenant is returned by key functions that find no tenant on a request.
	ErrNoTenant = errors.New("ratelimitd: request carries no tenant")
)

package controlplane

import "errors"

// Sentinel errors used by the service layer and mapped to HTTP status codes
// by the control plane HTTP handlers.
var (
	ErrAlreadyExists = errors.New("already exists")
	ErrNotFound      = errors.New("not found")
	ErrInvalidInput  = errors.New("invalid input")
)

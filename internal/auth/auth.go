// Package auth validates Sentry-compatible request signatures.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrMissingAuthorization is returned when a protected request omits auth.
	ErrMissingAuthorization = errors.New("missing authorization header")
	// ErrInvalidAuthorizationFormat is returned when the auth header is malformed.
	ErrInvalidAuthorizationFormat = errors.New("invalid authorization header format")
	// ErrInvalidSignature is returned when no shared secret validates the payload.
	ErrInvalidSignature = errors.New("invalid request signature")
)

// VerifyRequest verifies the raw request body against the Authorization header.
func VerifyRequest(body []byte, authorization string, sharedSecrets []string) error {
	if len(sharedSecrets) == 0 {
		return nil
	}
	if strings.TrimSpace(authorization) == "" {
		return ErrMissingAuthorization
	}
	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Rpcsignature") {
		return ErrInvalidAuthorizationFormat
	}
	versioned := parts[1]
	if !strings.HasPrefix(versioned, "rpc0:") {
		return fmt.Errorf("%w: expected rpc0 prefix", ErrInvalidAuthorizationFormat)
	}
	signatureHex := strings.TrimPrefix(versioned, "rpc0:")
	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return fmt.Errorf("%w: invalid hex", ErrInvalidAuthorizationFormat)
	}
	for _, secret := range sharedSecrets {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		if hmac.Equal(mac.Sum(nil), signature) {
			return nil
		}
	}
	return ErrInvalidSignature
}

// SignRequestBody creates an Authorization header for a request body.
func SignRequestBody(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "Rpcsignature rpc0:" + hex.EncodeToString(mac.Sum(nil))
}

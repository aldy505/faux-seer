package auth

import "testing"

func TestVerifyRequestAcceptsValidSignature(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	secret := "top-secret"
	header := SignRequestBody(body, secret)
	if err := VerifyRequest(body, header, []string{secret}); err != nil {
		t.Fatalf("expected signature to validate, got %v", err)
	}
}

func TestVerifyRequestRejectsInvalidSignature(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	if err := VerifyRequest(body, "Rpcsignature rpc0:deadbeef", []string{"top-secret"}); err == nil {
		t.Fatal("expected invalid signature error")
	}
}

func TestVerifyRequestRejectsMissingHeader(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	if err := VerifyRequest(body, "", []string{"top-secret"}); err != ErrMissingAuthorization {
		t.Fatalf("expected missing authorization error, got %v", err)
	}
}

func TestVerifyRequestSupportsSecretRotation(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	header := SignRequestBody(body, "new-secret")
	if err := VerifyRequest(body, header, []string{"old-secret", "new-secret"}); err != nil {
		t.Fatalf("expected rotated secret to validate, got %v", err)
	}
}

// Package monitoring verifies external monitoring provider connections.
package monitoring

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aldy505/faux-seer/internal/config"
)

// Service verifies monitoring provider connections.
type Service struct {
	cfg *config.Config
}

// New creates a monitoring service.
func New(cfg *config.Config) *Service {
	return &Service{cfg: cfg}
}

// VerifyResponse is the wire shape Sentry validates.
type VerifyResponse struct {
	ConnectionStatus string                `json:"connection_status"`
	Projects         []ProjectVerifyResult `json:"projects"`
	ErrorDetail      *string               `json:"error_detail"`
}

// ProjectVerifyResult is the per-project verification result.
type ProjectVerifyResult struct {
	GCPProjectID     string   `json:"gcp_project_id"`
	ConnectionStatus string   `json:"connection_status"`
	Services         []string `json:"services"`
	ErrorDetail      *string  `json:"error_detail"`
}

// VerifyRequest is the payload Sentry sends to verify a GCP connection.
type VerifyRequest struct {
	SentryServiceAccountEmail   string   `json:"sentry_sa_email"`
	CustomerServiceAccountEmail string   `json:"customer_sa_email"`
	GCPProjectIDs               []string `json:"gcp_project_ids"`
}

// VerifyConnection verifies a GCP connection.
//
// Every verification outcome is reported in the response rather than as an
// error: Sentry's setup flow reads connection_status and error_detail from a 200
// body, and a 5xx would only make its client raise. An error is returned only
// when the service is misconfigured in a way no response can describe.
func (s *Service) VerifyConnection(ctx context.Context, request VerifyRequest) (VerifyResponse, error) {
	// SIMULATED: When no GCP service-account JSON is configured, faux-seer
	// cannot authenticate against Google Cloud APIs. Returning a simulated
	// successful connection preserves Sentry's GCP integration setup flow —
	// the setup wizard only proceeds when it receives a 200 with
	// connection_status "connected".
	saJSON := strings.TrimSpace(s.cfg.GCPServiceAccountJSON)
	if saJSON == "" {
		return s.simulatedResponse(request.GCPProjectIDs), nil
	}

	sa, err := loadServiceAccount(saJSON)
	if err != nil {
		return s.failedResponse(request.GCPProjectIDs, fmt.Sprintf("load GCP service account: %v", err)), nil
	}
	token, err := s.fetchAccessToken(ctx, sa)
	if err != nil {
		return s.failedResponse(request.GCPProjectIDs, fmt.Sprintf("obtain GCP access token: %v", err)), nil
	}

	projects := make([]ProjectVerifyResult, 0, len(request.GCPProjectIDs))
	allConnected := true
	for _, projectID := range request.GCPProjectIDs {
		result, err := verifyProject(ctx, token, projectID)
		if err != nil {
			allConnected = false
			errDetail := err.Error()
			result = ProjectVerifyResult{
				GCPProjectID:     projectID,
				ConnectionStatus: "error",
				Services:         []string{},
				ErrorDetail:      &errDetail,
			}
		}
		projects = append(projects, result)
	}

	if allConnected {
		return VerifyResponse{ConnectionStatus: "connected", Projects: projects}, nil
	}
	message := "one or more projects failed verification"
	return VerifyResponse{ConnectionStatus: "error", Projects: projects, ErrorDetail: &message}, nil
}

// failedResponse reports a connection that could not be attempted at all, such
// as an unusable service account.
func (s *Service) failedResponse(projectIDs []string, detail string) VerifyResponse {
	projects := make([]ProjectVerifyResult, 0, len(projectIDs))
	for _, id := range projectIDs {
		errDetail := detail
		projects = append(projects, ProjectVerifyResult{
			GCPProjectID:     id,
			ConnectionStatus: "error",
			Services:         []string{},
			ErrorDetail:      &errDetail,
		})
	}
	return VerifyResponse{ConnectionStatus: "error", Projects: projects, ErrorDetail: &detail}
}

func (s *Service) simulatedResponse(projectIDs []string) VerifyResponse {
	projects := make([]ProjectVerifyResult, 0, len(projectIDs))
	for _, id := range projectIDs {
		projects = append(projects, ProjectVerifyResult{
			GCPProjectID:     id,
			ConnectionStatus: "connected",
			Services:         []string{},
			ErrorDetail:      nil,
		})
	}
	return VerifyResponse{
		ConnectionStatus: "connected",
		Projects:         projects,
		ErrorDetail:      nil,
	}
}

// serviceAccount represents the JSON key file fields needed for JWT signing.
type serviceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// loadServiceAccount parses a service-account JSON key. The value may be an
// inline JSON string or a filesystem path.
func loadServiceAccount(raw string) (*serviceAccount, error) {
	data := []byte(raw)
	// If it doesn't look like JSON, try reading it as a file path.
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		pathBytes, err := os.ReadFile(raw)
		if err != nil {
			return nil, fmt.Errorf("read service account file: %w", err)
		}
		data = pathBytes
	}
	var sa serviceAccount
	if err := json.Unmarshal(data, &sa); err != nil {
		return nil, fmt.Errorf("parse service account JSON: %w", err)
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("service account missing client_email or private_key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &sa, nil
}

// fetchAccessToken exchanges a signed JWT for an OAuth2 access token.
func (s *Service) fetchAccessToken(ctx context.Context, sa *serviceAccount) (string, error) {
	now := time.Now()
	claims := map[string]any{
		"iss":   sa.ClientEmail,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   sa.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal JWT claims: %w", err)
	}

	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block from private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1 as fallback.
		pk1, pk1Err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if pk1Err != nil {
			return "", fmt.Errorf("parse private key: %w (pkcs8: %v)", pk1Err, err)
		}
		key = pk1
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("private key is not RSA")
	}

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := header + "." + payload

	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	jwt := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	// Exchange the JWT for an access token.
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwt},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sa.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange JWT for token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}
	return tokenResp.AccessToken, nil
}

// verifyProject checks whether a GCP project is accessible and lists enabled
// services via the Cloud Resource Manager and Service Usage APIs.
func verifyProject(ctx context.Context, token, projectID string) (ProjectVerifyResult, error) {
	// Verify project access via Cloud Resource Manager.
	projectURL := fmt.Sprintf("https://cloudresourcemanager.googleapis.com/v1/projects/%s", projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, projectURL, nil)
	if err != nil {
		return ProjectVerifyResult{}, fmt.Errorf("create project request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ProjectVerifyResult{}, fmt.Errorf("check project %s: %w", projectID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ProjectVerifyResult{}, fmt.Errorf("project %s: HTTP %d: %s", projectID, resp.StatusCode, string(body))
	}

	// List enabled services via Service Usage API.
	servicesURL := fmt.Sprintf("https://serviceusage.googleapis.com/v1beta1/projects/%s/services?filter=state:ENABLED", projectID)
	sreq, err := http.NewRequestWithContext(ctx, http.MethodGet, servicesURL, nil)
	if err != nil {
		return ProjectVerifyResult{}, fmt.Errorf("create services request: %w", err)
	}
	sreq.Header.Set("Authorization", "Bearer "+token)

	sresp, err := http.DefaultClient.Do(sreq)
	if err != nil {
		return ProjectVerifyResult{}, fmt.Errorf("list services for %s: %w", projectID, err)
	}
	defer sresp.Body.Close()

	sbody, err := io.ReadAll(sresp.Body)
	if err != nil {
		return ProjectVerifyResult{}, fmt.Errorf("read services response for %s: %w", projectID, err)
	}

	var servicesResp struct {
		Services []struct {
			Name string `json:"name"`
		} `json:"services"`
	}
	if err := json.Unmarshal(sbody, &servicesResp); err != nil {
		return ProjectVerifyResult{}, fmt.Errorf("decode services for %s: %w", projectID, err)
	}

	serviceNames := make([]string, 0, len(servicesResp.Services))
	for _, svc := range servicesResp.Services {
		// Extract the short name from the full resource name
		// (projects/PROJECT/services/SERVICE).
		parts := strings.Split(svc.Name, "/")
		if len(parts) > 0 {
			serviceNames = append(serviceNames, parts[len(parts)-1])
		} else {
			serviceNames = append(serviceNames, svc.Name)
		}
	}

	return ProjectVerifyResult{
		GCPProjectID:     projectID,
		ConnectionStatus: "connected",
		Services:         serviceNames,
		ErrorDetail:      nil,
	}, nil
}

package avtkitcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	consolev1 "github.com/spatialwalk/open-platform-cli/api/generated/console/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("request failed with status %d: %s", e.StatusCode, e.Message)
}

type APIClient struct {
	baseURL    string
	httpClient *http.Client
	marshal    protojson.MarshalOptions
	unmarshal  protojson.UnmarshalOptions
}

func NewAPIClient(baseURL string) *APIClient {
	return &APIClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		marshal: protojson.MarshalOptions{},
		unmarshal: protojson.UnmarshalOptions{
			DiscardUnknown: true,
		},
	}
}

func (c *APIClient) CreateCLIAuthSession(ctx context.Context, req *consolev1.CreateCLIAuthSessionRequest) (*consolev1.CreateCLIAuthSessionResponse, error) {
	resp := &consolev1.CreateCLIAuthSessionResponse{}
	if err := c.do(ctx, http.MethodPost, "/v1/cli/auth/sessions", req, "", resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) ExchangeCLIAuthToken(ctx context.Context, req *consolev1.ExchangeCLIAuthTokenRequest) (*consolev1.ExchangeCLIAuthTokenResponse, error) {
	resp := &consolev1.ExchangeCLIAuthTokenResponse{}
	if err := c.do(ctx, http.MethodPost, "/v1/cli/auth/token", req, "", resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) RefreshCLIAuthToken(ctx context.Context, req *consolev1.RefreshCLIAuthTokenRequest) (*consolev1.RefreshCLIAuthTokenResponse, error) {
	resp := &consolev1.RefreshCLIAuthTokenResponse{}
	if err := c.do(ctx, http.MethodPost, "/v1/cli/auth/token:refresh", req, "", resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) RevokeCLIAuthToken(ctx context.Context, req *consolev1.RevokeCLIAuthTokenRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/cli/auth/token:revoke", req, "", &consolev1.RevokeCLIAuthTokenResponse{})
}

func (c *APIClient) GetMe(ctx context.Context, accessToken string) (*consolev1.ConsoleUser, error) {
	resp := &consolev1.GetMeResponse{}
	if err := c.do(ctx, http.MethodGet, "/v1/auth/me", nil, accessToken, resp); err != nil {
		return nil, err
	}
	if resp.GetUser() == nil {
		return nil, fmt.Errorf("get current user returned no user")
	}
	return resp.GetUser(), nil
}

func (c *APIClient) do(ctx context.Context, method, path string, request proto.Message, accessToken string, response proto.Message) error {
	fullURL := c.baseURL + path

	var body io.Reader
	if request != nil && method != http.MethodGet {
		payload, err := c.marshal.Marshal(request)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	if request != nil && method != http.MethodGet {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if token := strings.TrimSpace(accessToken); token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer httpResp.Body.Close()

	payload, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return &HTTPError{
			StatusCode: httpResp.StatusCode,
			Message:    parseHTTPErrorMessage(payload),
		}
	}

	if response == nil {
		return nil
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := c.unmarshal.Unmarshal(payload, response); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func parseHTTPErrorMessage(payload []byte) string {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return ""
	}

	var object map[string]any
	if err := json.Unmarshal(payload, &object); err == nil {
		for _, key := range []string{"error", "message", "detail"} {
			if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return strings.TrimSpace(string(payload))
}

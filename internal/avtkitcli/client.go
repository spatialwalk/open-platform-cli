package avtkitcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	consolev1 "github.com/spatialwalk/open-platform-cli/api/generated/console/v1"
	consolev2 "github.com/spatialwalk/open-platform-cli/api/generated/console/v2"
	jsonapiv1 "github.com/spatialwalk/open-platform-cli/api/generated/jsonapi/v1"
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
	if err := c.do(ctx, http.MethodPost, "/v1/cli/auth/sessions", nil, req, "", resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) ExchangeCLIAuthToken(ctx context.Context, req *consolev1.ExchangeCLIAuthTokenRequest) (*consolev1.ExchangeCLIAuthTokenResponse, error) {
	resp := &consolev1.ExchangeCLIAuthTokenResponse{}
	if err := c.do(ctx, http.MethodPost, "/v1/cli/auth/token", nil, req, "", resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) RefreshCLIAuthToken(ctx context.Context, req *consolev1.RefreshCLIAuthTokenRequest) (*consolev1.RefreshCLIAuthTokenResponse, error) {
	resp := &consolev1.RefreshCLIAuthTokenResponse{}
	if err := c.do(ctx, http.MethodPost, "/v1/cli/auth/token:refresh", nil, req, "", resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) RevokeCLIAuthToken(ctx context.Context, req *consolev1.RevokeCLIAuthTokenRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/cli/auth/token:revoke", nil, req, "", &consolev1.RevokeCLIAuthTokenResponse{})
}

func (c *APIClient) GetMe(ctx context.Context, accessToken string) (*consolev1.ConsoleUser, error) {
	resp := &consolev1.GetMeResponse{}
	if err := c.do(ctx, http.MethodGet, "/v1/auth/me", nil, nil, accessToken, resp); err != nil {
		return nil, err
	}
	if resp.GetUser() == nil {
		return nil, fmt.Errorf("get current user returned no user")
	}
	return resp.GetUser(), nil
}

func (c *APIClient) CreateApp(ctx context.Context, accessToken string, req *consolev1.AppServiceCreateAppRequest) (*consolev1.AppServiceCreateAppResponse, error) {
	resp := &consolev1.AppServiceCreateAppResponse{}
	if err := c.do(ctx, http.MethodPost, "/v1/apps", nil, req, accessToken, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) ListApps(ctx context.Context, accessToken string, req *consolev1.AppServiceListAppsRequest) (*consolev1.AppServiceListAppsResponse, error) {
	resp := &consolev1.AppServiceListAppsResponse{}
	if err := c.do(ctx, http.MethodGet, "/v1/apps", paginationQuery(req.GetPagination()), nil, accessToken, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) GetApp(ctx context.Context, accessToken, appID string) (*consolev1.AppServiceGetAppResponse, error) {
	resp := &consolev1.AppServiceGetAppResponse{}
	if err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(strings.TrimSpace(appID)), nil, nil, accessToken, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) DeleteApp(ctx context.Context, accessToken, appID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(strings.TrimSpace(appID)), nil, nil, accessToken, &consolev1.AppServiceDeleteAppResponse{})
}

func (c *APIClient) CreateAPIKey(ctx context.Context, accessToken, appID string) (*consolev1.AppServiceCreateAPIKeyResponse, error) {
	resp := &consolev1.AppServiceCreateAPIKeyResponse{}
	req := &consolev1.AppServiceCreateAPIKeyRequest{AppId: strings.TrimSpace(appID)}
	path := "/v1/apps/" + url.PathEscape(strings.TrimSpace(appID)) + "/api-keys"
	if err := c.do(ctx, http.MethodPost, path, nil, req, accessToken, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) ListAPIKeys(ctx context.Context, accessToken, appID string, req *consolev1.AppServiceListAPIKeysRequest) (*consolev1.AppServiceListAPIKeysResponse, error) {
	resp := &consolev1.AppServiceListAPIKeysResponse{}
	path := "/v1/apps/" + url.PathEscape(strings.TrimSpace(appID)) + "/api-keys"
	if err := c.do(ctx, http.MethodGet, path, paginationQuery(req.GetPagination()), nil, accessToken, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) DeleteAPIKey(ctx context.Context, accessToken, appID, apiKey string) error {
	path := "/v1/apps/" + url.PathEscape(strings.TrimSpace(appID)) + "/api-keys/" + url.PathEscape(strings.TrimSpace(apiKey))
	return c.do(ctx, http.MethodDelete, path, nil, nil, accessToken, &consolev1.AppServiceDeleteAPIKeyResponse{})
}

func (c *APIClient) CreateSessionToken(ctx context.Context, apiKey string, req *consolev1.CreateSessionTokenRequest) (*consolev1.CreateSessionTokenResponse, error) {
	resp := &consolev1.CreateSessionTokenResponse{}
	headers := http.Header{}
	headers.Set("X-API-KEY", strings.TrimSpace(apiKey))
	if err := c.doWithHeaders(ctx, http.MethodPost, "/v1/console/session-tokens", nil, req, "", headers, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) ListPublicAvatars(ctx context.Context, accessToken string, req *consolev2.ListPublicAvatarsRequest) (*consolev2.ListPublicAvatarsResponse, error) {
	resp := &consolev2.ListPublicAvatarsResponse{}
	if err := c.do(ctx, http.MethodGet, "/v2/console/public-avatars", paginationQuery(req.GetPagination()), nil, accessToken, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *APIClient) do(ctx context.Context, method, path string, query url.Values, request proto.Message, accessToken string, response proto.Message) error {
	return c.doWithHeaders(ctx, method, path, query, request, accessToken, nil, response)
}

func (c *APIClient) doWithHeaders(ctx context.Context, method, path string, query url.Values, request proto.Message, accessToken string, headers http.Header, response proto.Message) error {
	fullURL := c.baseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

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
	for key, values := range headers {
		httpReq.Header[textproto.CanonicalMIMEHeaderKey(key)] = values
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

func paginationQuery(pagination *jsonapiv1.PaginationRequest) url.Values {
	if pagination == nil {
		return nil
	}

	values := url.Values{}
	if pagination.GetPageSize() > 0 {
		values.Set("pagination.pageSize", strconv.Itoa(int(pagination.GetPageSize())))
	}
	if token := strings.TrimSpace(pagination.GetPageToken()); token != "" {
		values.Set("pagination.pageToken", token)
	}
	if len(values) == 0 {
		return nil
	}
	return values
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

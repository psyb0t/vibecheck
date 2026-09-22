package klhayyent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/psyb0t/aichteeteapee"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxscope"
	"golang.org/x/net/publicsuffix"
)

const responseReadExtraByte int64 = 1

// Client is safe for concurrent use. Its configuration is immutable.
type Client struct {
	baseURL             *url.URL
	httpClient          *http.Client
	defaultHeaders      http.Header
	maxResponseBodySize int64
	dependencyName      string
}

// New constructs a client for a trusted absolute HTTP or HTTPS base URL.
// Per-request targets remain relative to this origin and path prefix.
func New(baseURL string, options ...Option) (*Client, error) {
	parsedBaseURL, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "create client")
	}

	config := defaultClientConfig()

	if err := applyClientOptions(&config, options); err != nil {
		return nil, ctxerrors.Wrap(err, "create client")
	}

	httpClient, err := configuredHTTPClient(&config, parsedBaseURL)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "create client")
	}

	return &Client{
		baseURL:             parsedBaseURL,
		httpClient:          httpClient,
		defaultHeaders:      config.defaultHeaders.Clone(),
		maxResponseBodySize: config.maxResponseBodySize,
		dependencyName:      config.dependencyName,
	}, nil
}

func applyClientOptions(config *clientConfig, options []Option) error {
	for _, option := range options {
		if option == nil {
			continue
		}

		if err := option(config); err != nil {
			return ctxerrors.Wrap(err, "apply client option")
		}
	}

	return nil
}

func configuredHTTPClient(
	config *clientConfig,
	baseURL *url.URL,
) (*http.Client, error) {
	jar := config.httpClient.Jar
	if config.hasCookieJar {
		jar = config.cookieJar
	}

	if jar == nil {
		createdJar, err := cookiejar.New(&cookiejar.Options{
			PublicSuffixList: publicsuffix.List,
		})
		if err != nil {
			return nil, ctxerrors.Wrap(err, "create cookie jar")
		}

		jar = createdJar
	}

	httpClient := *config.httpClient
	httpClient.Jar = jar
	httpClient.CheckRedirect = func(
		request *http.Request,
		via []*http.Request,
	) error {
		return checkRedirect(
			baseURL,
			request,
			via,
			config.checkRedirect,
			config.httpClient.CheckRedirect,
		)
	}

	return &httpClient, nil
}

func checkRedirect(
	baseURL *url.URL,
	request *http.Request,
	via []*http.Request,
	configuredPolicy func(*http.Request, []*http.Request) error,
	originalPolicy func(*http.Request, []*http.Request) error,
) error {
	if len(via) >= defaultMaxRedirects || !sameOrigin(baseURL, request.URL) {
		return ctxerrors.Wrap(
			ErrInvalidRequestTarget,
			"reject unsafe redirect",
		)
	}

	policy := configuredPolicy
	if policy == nil {
		policy = originalPolicy
	}

	if policy != nil {
		if err := policy(request, via); err != nil {
			return err
		}
	}

	if !sameOrigin(baseURL, request.URL) {
		return ctxerrors.Wrap(
			ErrInvalidRequestTarget,
			"reject redirect policy URL mutation",
		)
	}

	return nil
}

// CookieJar returns the jar used by the client.
func (c *Client) CookieJar() http.CookieJar {
	if c == nil || c.httpClient == nil {
		return nil
	}

	return c.httpClient.Jar
}

// Get performs a GET request.
func (c *Client) Get(
	ctx context.Context,
	target string,
	responseBody any,
	options ...RequestOption,
) (*Response, error) {
	return c.Do(ctx, http.MethodGet, target, nil, responseBody, options...)
}

// Head performs a HEAD request.
func (c *Client) Head(
	ctx context.Context,
	target string,
	options ...RequestOption,
) (*Response, error) {
	return c.Do(ctx, http.MethodHead, target, nil, nil, options...)
}

// Options performs an OPTIONS request.
func (c *Client) Options(
	ctx context.Context,
	target string,
	responseBody any,
	options ...RequestOption,
) (*Response, error) {
	return c.Do(ctx, http.MethodOptions, target, nil, responseBody, options...)
}

// Delete performs a DELETE request.
func (c *Client) Delete(
	ctx context.Context,
	target string,
	responseBody any,
	options ...RequestOption,
) (*Response, error) {
	return c.Do(ctx, http.MethodDelete, target, nil, responseBody, options...)
}

// Post performs a POST request.
func (c *Client) Post(
	ctx context.Context,
	target string,
	requestBody, responseBody any,
	options ...RequestOption,
) (*Response, error) {
	return c.Do(
		ctx,
		http.MethodPost,
		target,
		requestBody,
		responseBody,
		options...,
	)
}

// Put performs a PUT request.
func (c *Client) Put(
	ctx context.Context,
	target string,
	requestBody, responseBody any,
	options ...RequestOption,
) (*Response, error) {
	return c.Do(
		ctx,
		http.MethodPut,
		target,
		requestBody,
		responseBody,
		options...,
	)
}

// Patch performs a PATCH request.
func (c *Client) Patch(
	ctx context.Context,
	target string,
	requestBody, responseBody any,
	options ...RequestOption,
) (*Response, error) {
	return c.Do(
		ctx,
		http.MethodPatch,
		target,
		requestBody,
		responseBody,
		options...,
	)
}

// Do executes one bounded request. Non-2xx responses return an HTTPError.
func (c *Client) Do(
	ctx context.Context,
	method, target string,
	requestBody, responseBody any,
	options ...RequestOption,
) (*Response, error) {
	if err := c.validateRequest(ctx, method); err != nil {
		return nil, ctxerrors.Wrap(err, "validate client request")
	}

	request, err := c.newRequest(
		ctx,
		method,
		target,
		requestBody,
		options,
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "prepare client request")
	}

	httpResponse, startedAt, err := c.executeRequest(ctx, request)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "perform client request")
	}

	defer c.closeResponseBody(ctx, method, httpResponse)

	response, err := c.readResponse(httpResponse)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "receive client response")
	}

	c.logCompletedRequest(ctx, method, response.StatusCode, startedAt)

	if !isSuccessStatus(response.StatusCode) {
		return response, newHTTPError(response)
	}

	if err := decodeResponseBody(response.Body, responseBody); err != nil {
		return response, ctxerrors.Wrap(err, "receive client response")
	}

	return response, nil
}

func (c *Client) validateRequest(ctx context.Context, method string) error {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return ctxerrors.Wrap(ErrInvalidOption, "use nil client")
	}

	if ctx == nil {
		return ctxerrors.Wrap(ErrInvalidOption, "use nil context")
	}

	if method == "" {
		return ctxerrors.Wrap(ErrInvalidOption, "use empty HTTP method")
	}

	return nil
}

func (c *Client) newRequest(
	ctx context.Context,
	method, target string,
	requestBody any,
	options []RequestOption,
) (*http.Request, error) {
	requestURL, err := c.buildRequestURL(target)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build request URL")
	}

	bodyReader, contentType, err := encodeRequestBody(requestBody)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "prepare request body")
	}

	requestConfig, err := c.buildRequestConfig(options)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "configure HTTP request")
	}

	query := mergeQuery(requestURL.Query(), requestConfig.query)

	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(
		ctx,
		method,
		requestURL.String(),
		bodyReader,
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "create HTTP request")
	}

	request.Header = requestConfig.headers.Clone()
	if contentType != "" && request.Header.Get(
		aichteeteapee.HeaderNameContentType,
	) == "" {
		request.Header.Set(aichteeteapee.HeaderNameContentType, contentType)
	}

	requestID, hasRequestID := ctx.Value(
		aichteeteapee.ContextKeyRequestID,
	).(string)
	if hasRequestID && requestID != "" {
		request.Header.Set(aichteeteapee.HeaderNameXRequestID, requestID)
	}

	return request, nil
}

func (c *Client) buildRequestConfig(
	options []RequestOption,
) (requestConfig, error) {
	config := requestConfig{
		headers: c.defaultHeaders.Clone(),
		query:   make(url.Values),
	}

	for _, option := range options {
		if option == nil {
			continue
		}

		if err := option(&config); err != nil {
			return requestConfig{}, ctxerrors.Wrap(err, "apply request option")
		}
	}

	return config, nil
}

func mergeQuery(baseValues, requestValues url.Values) url.Values {
	for key, values := range requestValues {
		for _, value := range values {
			baseValues.Add(key, value)
		}
	}

	return baseValues
}

func (c *Client) executeRequest(
	ctx context.Context,
	request *http.Request,
) (*http.Response, time.Time, error) {
	startedAt := time.Now()

	httpResponse, err := c.httpClient.Do(request)
	if err != nil {
		ctxscope.GetLogger(ctx).Error(
			"HTTP client request failed",
			"dependency", c.dependencyName,
			"method", request.Method,
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"outcome", "error",
			"err", aichteeteapee.ErrAPIError,
		)

		return nil, time.Time{}, ctxerrors.Wrap(
			sanitizeTransportError(err),
			"execute HTTP request",
		)
	}

	return httpResponse, startedAt, nil
}

func (c *Client) closeResponseBody(
	ctx context.Context,
	method string,
	httpResponse *http.Response,
) {
	if closeErr := httpResponse.Body.Close(); closeErr != nil {
		ctxscope.GetLogger(ctx).Warn(
			"HTTP response body close failed",
			"dependency", c.dependencyName,
			"method", method,
			"status", httpResponse.StatusCode,
			"err", closeErr,
		)
	}
}

func (c *Client) logCompletedRequest(
	ctx context.Context,
	method string,
	statusCode int,
	startedAt time.Time,
) {
	ctxscope.GetLogger(ctx).Info(
		"HTTP client request completed",
		"dependency", c.dependencyName,
		"method", method,
		"status", statusCode,
		"duration_ms", time.Since(startedAt).Milliseconds(),
		"outcome", responseOutcome(statusCode),
	)
}

func parseBaseURL(rawBaseURL string) (*url.URL, error) {
	parsedBaseURL, err := url.Parse(rawBaseURL)
	if err != nil {
		return nil, ctxerrors.Wrap(
			errors.Join(ErrInvalidBaseURL, err),
			"parse base URL",
		)
	}

	if parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https" {
		return nil, ctxerrors.Wrap(
			ErrInvalidBaseURL,
			"validate base URL scheme",
		)
	}

	if parsedBaseURL.Host == "" || parsedBaseURL.User != nil ||
		parsedBaseURL.RawQuery != "" || parsedBaseURL.Fragment != "" {
		return nil, ctxerrors.Wrap(ErrInvalidBaseURL, "validate base URL")
	}

	return parsedBaseURL, nil
}

func (c *Client) buildRequestURL(target string) (*url.URL, error) {
	if hasUnsafeTargetText(target) {
		return nil, ctxerrors.Wrap(
			ErrInvalidRequestTarget,
			"validate request target",
		)
	}

	parsedTarget, err := url.Parse(target)
	if err != nil {
		return nil, ctxerrors.Wrap(
			errors.Join(ErrInvalidRequestTarget, err),
			"parse request target",
		)
	}

	if isInvalidParsedTarget(parsedTarget) {
		return nil, ctxerrors.Wrap(
			ErrInvalidRequestTarget,
			"validate request target",
		)
	}

	requestURL := *c.baseURL
	if parsedTarget.Path != "" {
		requestURL.Path = path.Join(
			c.baseURL.Path,
			strings.TrimPrefix(parsedTarget.Path, "/"),
		)
		requestURL.RawPath = ""
	}

	requestURL.RawQuery = parsedTarget.RawQuery

	return &requestURL, nil
}

func hasUnsafeTargetText(target string) bool {
	return strings.HasPrefix(target, "//") ||
		strings.ContainsRune(target, '\\') ||
		strings.IndexFunc(target, unicode.IsControl) >= 0
}

func isInvalidParsedTarget(target *url.URL) bool {
	return target.IsAbs() || target.Host != "" || target.User != nil ||
		target.Fragment != "" || hasTraversalSegment(target.EscapedPath())
}

func hasTraversalSegment(escapedPath string) bool {
	decodedPath := escapedPath
	for range 3 {
		unescapedPath, err := url.PathUnescape(decodedPath)
		if err != nil {
			return true
		}

		for segment := range strings.SplitSeq(unescapedPath, "/") {
			if segment == ".." {
				return true
			}
		}

		if unescapedPath == decodedPath {
			return false
		}

		decodedPath = unescapedPath
	}

	return strings.Contains(strings.ToLower(decodedPath), "%2e")
}

func encodeRequestBody(requestBody any) (io.Reader, string, error) {
	if requestBody == nil {
		return nil, "", nil
	}

	switch body := requestBody.(type) {
	case io.Reader:
		return body, "", nil
	case []byte:
		return bytes.NewReader(body), aichteeteapee.ContentTypeOctetStream, nil
	case string:
		return strings.NewReader(body), aichteeteapee.ContentTypeTextPlain, nil
	case url.Values:
		return strings.NewReader(body.Encode()),
			aichteeteapee.ContentTypeApplicationFormURLEncoded,
			nil
	default:
		encodedBody, err := json.Marshal(body)
		if err != nil {
			return nil, "", ctxerrors.Wrap(err, "encode JSON request body")
		}

		return bytes.NewReader(encodedBody), aichteeteapee.ContentTypeJSON, nil
	}
}

func (c *Client) readResponse(httpResponse *http.Response) (*Response, error) {
	body, err := io.ReadAll(io.LimitReader(
		httpResponse.Body,
		c.maxResponseBodySize+responseReadExtraByte,
	))
	if err != nil {
		return nil, ctxerrors.Wrap(err, "read HTTP response body")
	}

	if int64(len(body)) > c.maxResponseBodySize {
		return nil, ctxerrors.Wrap(
			ErrResponseBodyTooLarge,
			"read HTTP response body",
		)
	}

	return &Response{
		StatusCode: httpResponse.StatusCode,
		Header:     httpResponse.Header.Clone(),
		Body:       body,
	}, nil
}

func decodeResponseBody(body []byte, target any) error {
	if target == nil || len(body) == 0 {
		return nil
	}

	if writer, ok := target.(io.Writer); ok {
		if _, err := writer.Write(body); err != nil {
			return ctxerrors.Wrap(err, "write HTTP response body")
		}

		return nil
	}

	if bytesTarget, ok := target.(*[]byte); ok {
		*bytesTarget = append((*bytesTarget)[:0], body...)

		return nil
	}

	if err := json.Unmarshal(body, target); err != nil {
		return ctxerrors.Wrap(err, "decode JSON response body")
	}

	return nil
}

func newHTTPError(response *Response) *HTTPError {
	errorResponse := aichteeteapee.ErrorResponse{}
	if err := json.Unmarshal(response.Body, &errorResponse); err != nil ||
		errorResponse.Code == "" {
		errorResponse = aichteeteapee.ErrorResponse{
			Code:    aichteeteapee.ErrorCodeFromHTTPStatus(response.StatusCode),
			Message: http.StatusText(response.StatusCode),
		}
	}

	return &HTTPError{
		Response:      response,
		ErrorResponse: errorResponse,
		cause:         statusError(response.StatusCode),
	}
}

func statusError(statusCode int) error {
	switch statusCode {
	case http.StatusBadRequest:
		return aichteeteapee.ErrBadRequest
	case http.StatusUnauthorized:
		return aichteeteapee.ErrUnauthorized
	case http.StatusForbidden:
		return aichteeteapee.ErrForbidden
	case http.StatusNotFound:
		return aichteeteapee.ErrNotFound
	case http.StatusMethodNotAllowed:
		return aichteeteapee.ErrMethodNotAllowed
	case http.StatusConflict:
		return aichteeteapee.ErrConflict
	case http.StatusGone:
		return aichteeteapee.ErrGone
	default:
		return serverStatusError(statusCode)
	}
}

func serverStatusError(statusCode int) error {
	switch statusCode {
	case http.StatusUnprocessableEntity:
		return aichteeteapee.ErrUnprocessableEntity
	case http.StatusTooManyRequests:
		return aichteeteapee.ErrTooManyRequests
	case http.StatusInternalServerError:
		return aichteeteapee.ErrInternalServer
	case http.StatusBadGateway:
		return aichteeteapee.ErrBadGateway
	case http.StatusServiceUnavailable:
		return aichteeteapee.ErrServiceUnavailable
	case http.StatusGatewayTimeout:
		return aichteeteapee.ErrGatewayTimeout
	default:
		return aichteeteapee.ErrUnexpectedResponseStatus
	}
}

func isSuccessStatus(statusCode int) bool {
	return statusCode >= http.StatusOK &&
		statusCode < http.StatusMultipleChoices
}

func sameOrigin(baseURL, requestURL *url.URL) bool {
	return strings.EqualFold(baseURL.Scheme, requestURL.Scheme) &&
		strings.EqualFold(baseURL.Host, requestURL.Host)
}

func responseOutcome(statusCode int) string {
	if isSuccessStatus(statusCode) {
		return "success"
	}

	return "http_error"
}

func sanitizeTransportError(err error) error {
	for {
		var urlError *url.Error
		if !errors.As(err, &urlError) || urlError.Err == nil {
			return err
		}

		err = urlError.Err
	}
}

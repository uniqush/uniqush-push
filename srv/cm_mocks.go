/**
 * Mocks used by tests of both GCM and FCM.
 */
package srv

import (
	"bytes"
	cm "github.com/uniqush/uniqush-push/srv/cloud_messaging"
	"io"
	"net/http"

	// This is a collection of libraries for other tests
	_ "testing"
)

type mockCMHTTPClient struct {
	status       int
	responseBody []byte
	headers      map[string]string
	requestError error
	// Mocks for responses given json encoded request, TODO write expectations.
	// mockCMResponses map[string]string
	performed []*mockCMResponse
}

type mockCMResponse struct {
	impl    *bytes.Reader
	closed  bool
	request *http.Request
}

var _ io.ReadCloser = &mockCMResponse{}

func newMockCMResponse(contents []byte, request *http.Request) *mockCMResponse {
	return &mockCMResponse{
		impl:    bytes.NewReader(contents),
		closed:  false,
		request: request,
	}
}

func (r *mockCMResponse) Read(p []byte) (n int, err error) {
	return r.impl.Read(p)
}

func (r *mockCMResponse) Close() error {
	r.closed = true
	return nil
}

func (c *mockCMHTTPClient) Do(request *http.Request) (*http.Response, error) {
	h := http.Header{}
	for k, v := range c.headers {
		h.Set(k, v)
	}
	body := newMockCMResponse(c.responseBody, request)
	result := &http.Response{
		StatusCode: c.status,
		Body:       body,
		Header:     h,
	}
	c.performed = append(c.performed, body)
	return result, c.requestError
}

var _ cm.HTTPClient = &mockCMHTTPClient{}

package main

import (
	"net/http"
	"os"
	"strings"
	"time"
)

// uniqushHTTPClient returns a client for the uniqush REST API that sends the
// bearer token in tokenFile, or none when tokenFile is "", for a uniqush with
// [WebFrontend] token_file set.
func uniqushHTTPClient(timeout time.Duration, tokenFile string) (*http.Client, error) {
	client := &http.Client{Timeout: timeout}
	if tokenFile == "" {
		return client, nil
	}
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, err
	}
	client.Transport = bearerTransport{token: strings.TrimSpace(string(data))}
	return client, nil
}

type bearerTransport struct{ token string }

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(request)
}

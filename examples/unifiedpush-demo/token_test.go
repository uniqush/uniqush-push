package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUniqushHTTPClientSendsTheTokenOnlyWhenGivenOne(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer server.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for tokenFile, want := range map[string]string{"": "", tokenFile: "Bearer s3cret"} {
		client, err := uniqushHTTPClient(time.Second, tokenFile)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if got != want {
			t.Errorf("token file %q: expected Authorization %q, got %q", tokenFile, want, got)
		}
	}
}

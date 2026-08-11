package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadOrCreateVAPIDKeysRefusesToOverwrite is the important one.
//
// The tempting shape here is `if data, err := os.ReadFile(path); err == nil`,
// which reads *any* failure as "no keys yet" and generates a fresh pair. A
// permissions problem or a transient I/O error would then silently replace the
// existing key file -- and since the public key is baked into every
// subscription a browser ever made, that invalidates all of them at once, with
// nothing in the log but a cheerful "Generated new VAPID keys".
func TestLoadOrCreateVAPIDKeysRefusesToOverwrite(t *testing.T) {
	t.Run("generates when the file genuinely does not exist", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "vapid-keys.json")
		keys, err := loadOrCreateVAPIDKeys(path)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if keys.Public == "" || keys.Private == "" {
			t.Fatal("Expected a generated key pair")
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Expected the keys to be written to disk: %v", err)
		}
		// The private key is a credential.
		if mode := info.Mode().Perm(); mode != 0600 {
			t.Errorf("Expected mode 0600, got %o", mode)
		}
	})

	t.Run("reuses an existing pair rather than regenerating", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "vapid-keys.json")
		first, err := loadOrCreateVAPIDKeys(path)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		second, err := loadOrCreateVAPIDKeys(path)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if first.Public != second.Public || first.Private != second.Private {
			t.Error("The key pair must be stable across restarts, or every existing " +
				"subscription is invalidated")
		}
	})

	t.Run("an unreadable file is a hard error, not a reason to regenerate", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root, which can read anything")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "vapid-keys.json")
		if err := os.WriteFile(path, []byte(`{"vapidpublickey":"a","vapidprivatekey":"b"}`), 0600); err != nil {
			t.Fatalf("Could not seed the key file: %v", err)
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatalf("Could not make the file unreadable: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0600) })

		_, err := loadOrCreateVAPIDKeys(path)
		if err == nil {
			t.Fatal("Expected an error rather than a silently regenerated key pair")
		}
		if !strings.Contains(err.Error(), "refusing to generate") {
			t.Errorf("Expected the error to explain why it refused, got: %v", err)
		}
		// The original file must be untouched.
		if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
			t.Fatalf("Could not restore permissions: %v", chmodErr)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("Could not re-read the key file: %v", readErr)
		}
		if !strings.Contains(string(data), `"vapidpublickey":"a"`) {
			t.Errorf("The existing key file was overwritten: %s", data)
		}
	})

	t.Run("a corrupt file is a hard error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "vapid-keys.json")
		if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
			t.Fatalf("Could not seed the key file: %v", err)
		}
		if _, err := loadOrCreateVAPIDKeys(path); err == nil {
			t.Error("Expected an error for a corrupt key file")
		}
	})

	t.Run("a file missing a key is a hard error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "vapid-keys.json")
		if err := os.WriteFile(path, []byte(`{"vapidpublickey":"a"}`), 0600); err != nil {
			t.Fatalf("Could not seed the key file: %v", err)
		}
		if _, err := loadOrCreateVAPIDKeys(path); err == nil {
			t.Error("Expected an error for a key file missing the private key")
		}
	})
}

// TestUniqushResponsesArePropagated covers the other half: a debugging tool
// that reports success while uniqush was returning an error is worse than one
// that does nothing, because it sends you looking in the wrong place.
func TestUniqushResponsesArePropagated(t *testing.T) {
	testCases := []struct {
		name      string
		status    int
		body      string
		expectErr bool
	}{
		{name: "200 is passed through", status: 200, body: `{"status":0}`},
		{name: "500 is an error", status: 500, body: "internal error", expectErr: true},
		{name: "404 is an error", status: 404, body: "not found", expectErr: true},
		{name: "400 is an error", status: 400, body: "bad request", expectErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()

			previous := *uniqushURL
			*uniqushURL = server.URL
			defer func() { *uniqushURL = previous }()

			d := &demo{client: server.Client()}

			// Both verbs must behave the same way; the bug this guards against
			// was the GET path having its own, laxer handling.
			for verb, call := range map[string]func() (string, error){
				"POST": func() (string, error) { return d.callUniqush("/subscribe", nil) },
				"GET":  func() (string, error) { return d.getUniqush("/subscriptions", nil) },
			} {
				body, err := call()
				if testCase.expectErr {
					if err == nil {
						t.Errorf("%s: expected an error for HTTP %d, got body %q", verb, testCase.status, body)
						continue
					}
					if !strings.Contains(err.Error(), testCase.body) {
						t.Errorf("%s: expected the error to include uniqush's response %q, got: %v",
							verb, testCase.body, err)
					}
					continue
				}
				if err != nil {
					t.Errorf("%s: unexpected error: %v", verb, err)
				}
				if body != testCase.body {
					t.Errorf("%s: expected body %q, got %q", verb, testCase.body, body)
				}
			}
		})
	}
}

// TestGetUniqushBuildsAQuery checks the query string is encoded rather than
// concatenated, which is what the hand-rolled Sprintf it replaced was doing.
func TestGetUniqushBuildsAQuery(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query().Get("subscriber")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	previous := *uniqushURL
	*uniqushURL = server.URL
	defer func() { *uniqushURL = previous }()

	d := &demo{client: server.Client()}
	if _, err := d.getUniqush("/subscriptions", map[string][]string{
		"subscriber": {"user with spaces & ampersand"},
	}); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if seen != "user with spaces & ampersand" {
		t.Errorf("Expected the subscriber to survive encoding, got %q", seen)
	}
}

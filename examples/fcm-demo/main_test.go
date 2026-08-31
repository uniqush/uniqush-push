package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes a config file next to a fake service account and returns
// the config path.
func writeConfig(t *testing.T, settings map[string]interface{}, withCredentials bool) string {
	t.Helper()

	dir := t.TempDir()
	if withCredentials {
		if err := os.WriteFile(filepath.Join(dir, "service-account.json"), []byte(`{}`), 0600); err != nil {
			t.Fatalf("Could not write the fake service account: %v", err)
		}
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("Could not encode the config: %v", err)
	}
	path := filepath.Join(dir, "fcm-demo.json")
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatalf("Could not write the config: %v", err)
	}
	return path
}

func completeConfig() map[string]interface{} {
	return map[string]interface{}{
		"projectId":       "my-project",
		"credentialsFile": "service-account.json",
		"web": map[string]string{
			"apiKey":            "AIzaTest",
			"messagingSenderId": "123456789012",
			"appId":             "1:123456789012:web:abc",
			"vapidKey":          "BNtest",
		},
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("resolves credentialsFile relative to the config file", func(t *testing.T) {
		// The natural mistake is to resolve it against the process's working
		// directory, which works when you run `go run .` from this directory
		// and breaks the moment anyone passes -config with a path.
		path := writeConfig(t, completeConfig(), true)
		settings, err := loadConfig(path)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !filepath.IsAbs(settings.CredentialsFile) {
			t.Errorf("Expected an absolute credentials path, got %q", settings.CredentialsFile)
		}
		if got, want := filepath.Dir(settings.CredentialsFile), filepath.Dir(path); got != want {
			t.Errorf("Expected the credentials to resolve into %q, got %q", want, got)
		}
	})

	t.Run("reports every missing field at once", func(t *testing.T) {
		// Reporting one field per restart turns filling in the config into a
		// guessing game, which is the opposite of what a setup tool should do.
		path := writeConfig(t, map[string]interface{}{"projectId": "my-project"}, true)
		_, err := loadConfig(path)
		if err == nil {
			t.Fatal("Expected an error for an incomplete config")
		}
		for _, field := range []string{"credentialsFile", "web.apiKey", "web.appId", "web.vapidKey"} {
			if !strings.Contains(err.Error(), field) {
				t.Errorf("Expected the error to name %s, got: %v", field, err)
			}
		}
	})

	t.Run("rejects a credentialsFile that is missing", func(t *testing.T) {
		// uniqush reads this file in its own process, so a path that does not
		// resolve here will not resolve there either. Catching it now beats an
		// opaque /addpsp failure.
		path := writeConfig(t, completeConfig(), false)
		if _, err := loadConfig(path); err == nil {
			t.Error("Expected an error when the service account is missing")
		}
	})

	t.Run("rejects a credentialsFile that exists but cannot be read", func(t *testing.T) {
		// The case that separates opening the file from stat-ing it. os.Stat
		// succeeds here -- the file is there, its metadata is readable -- and
		// the permissions mistake would then surface much later as a failed
		// /addpsp. This test is the reason loadConfig opens it.
		if os.Geteuid() == 0 {
			t.Skip("running as root, which ignores the permission bits this test sets")
		}

		path := writeConfig(t, completeConfig(), true)
		credentials := filepath.Join(filepath.Dir(path), "service-account.json")
		if err := os.Chmod(credentials, 0000); err != nil {
			t.Fatalf("Could not make the service account unreadable: %v", err)
		}
		// Restored so the temp directory can be cleaned up.
		t.Cleanup(func() { _ = os.Chmod(credentials, 0600) })

		// Guard against a filesystem that does not enforce this, rather than
		// reporting a uniqush bug that is not there.
		if handle, err := os.Open(credentials); err == nil {
			handle.Close()
			t.Skip("this filesystem does not enforce the permission bits")
		}

		if _, err := loadConfig(path); err == nil {
			t.Error("Expected an error for a service account that exists but cannot be read; " +
				"os.Stat would have accepted it")
		}
	})

	t.Run("derives projectId and authDomain for the browser", func(t *testing.T) {
		path := writeConfig(t, completeConfig(), true)
		settings, err := loadConfig(path)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if settings.Web.ProjectID != "my-project" {
			t.Errorf("Expected web.projectId to default to the project id, got %q", settings.Web.ProjectID)
		}
		if settings.Web.AuthDomain != "my-project.firebaseapp.com" {
			t.Errorf("Expected a derived authDomain, got %q", settings.Web.AuthDomain)
		}
	})

	t.Run("explains itself when the file does not exist", func(t *testing.T) {
		_, err := loadConfig(filepath.Join(t.TempDir(), "absent.json"))
		if err == nil {
			t.Fatal("Expected an error")
		}
		if !strings.Contains(err.Error(), "fcm-demo.example.json") {
			t.Errorf("Expected the error to point at the example config, got: %v", err)
		}
	})
}

// TestServiceAccountIsNeverSentToTheBrowser is the security-relevant one.
//
// The config struct deliberately keeps the service account path out of the Web
// half, because that half is serialized straight to the page. A refactor that
// moved or embedded it would leak the location of a credential that authorises
// sending to every device in the project.
func TestServiceAccountIsNeverSentToTheBrowser(t *testing.T) {
	path := writeConfig(t, completeConfig(), true)
	settings, err := loadConfig(path)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	d := &demo{config: settings, client: http.DefaultClient}
	recorder := httptest.NewRecorder()
	d.handleConfig(recorder, httptest.NewRequest(http.MethodGet, "/api/config", nil))

	body := recorder.Body.String()
	if strings.Contains(body, "service-account") || strings.Contains(body, settings.CredentialsFile) {
		t.Errorf("The config endpoint leaked the service account path: %s", body)
	}
	// Sanity check that it is returning the web config at all, so the test
	// cannot pass by returning nothing.
	if !strings.Contains(body, "vapidKey") {
		t.Errorf("Expected the web config to include vapidKey, got: %s", body)
	}
}

// TestUniqushErrorsAreNotReportedAsSuccess guards the failure mode that makes a
// debugging tool actively harmful: telling the page everything worked while
// uniqush was returning an error.
func TestUniqushErrorsAreNotReportedAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "NoSuchService", http.StatusBadRequest)
	}))
	defer server.Close()

	original := *uniqushURL
	*uniqushURL = server.URL
	defer func() { *uniqushURL = original }()

	d := &demo{client: server.Client()}
	if _, err := d.callUniqush("/push", nil); err == nil {
		t.Error("Expected an HTTP 400 from uniqush to be an error")
	}
	if _, err := d.getUniqush("/subscriptions", nil); err == nil {
		t.Error("Expected an HTTP 400 from uniqush to be an error")
	}
}

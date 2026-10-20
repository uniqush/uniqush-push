package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uniqush/uniqush-push/conf"
	"github.com/uniqush/uniqush-push/log"
	"github.com/uniqush/uniqush-push/push"
)

func TestBearerToken(t *testing.T) {
	const token = "s3cret-token"
	testCases := []struct {
		name          string
		token         string
		path          string
		authorization string
		wantStatus    int
	}{
		{"no token configured needs no header", "", VersionInfoURL, "", http.StatusOK},
		{"missing header", token, VersionInfoURL, "", http.StatusUnauthorized},
		{"wrong token", token, VersionInfoURL, "Bearer guess-0", http.StatusUnauthorized},
		{"token with a suffix", token, VersionInfoURL, "Bearer " + token + "x", http.StatusUnauthorized},
		{"token without a scheme", token, VersionInfoURL, token, http.StatusUnauthorized},
		{"wrong scheme", token, VersionInfoURL, "Basic " + token, http.StatusUnauthorized},
		{"right token", token, VersionInfoURL, "Bearer " + token, http.StatusOK},
		{"scheme is case-insensitive", token, VersionInfoURL, "bearer " + token, http.StatusOK},
		{"subscriptions need it", token, QuerySubscriptionsURL, "", http.StatusUnauthorized},
		{"stop needs it", token, StopProgramURL, "", http.StatusUnauthorized},
		{"metrics need it", token, MetricsURL, "", http.StatusUnauthorized},
		{"metrics with it", token, MetricsURL, "Bearer " + token, http.StatusOK},
		{"health is exempt", token, HealthURL, "", http.StatusOK},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			psm := push.GetPushServiceManager()
			var logged bytes.Buffer
			loggers := silentLoggers()
			loggers[LoggerWeb] = log.NewLogger(&logged, "[test]", log.LevelWarn)
			api := NewRestAPI(psm, loggers, "uniqush-push test", NewPushBackEnd(psm, &healthDatabase{}, silentLoggers()))
			if testCase.token != "" {
				api.requireToken(testCase.token)
			}

			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			if testCase.authorization != "" {
				request.Header.Set("Authorization", testCase.authorization)
			}
			recorder := httptest.NewRecorder()
			api.ServeHTTP(recorder, request)

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("Expected %d, got %d: %s", testCase.wantStatus, recorder.Code, recorder.Body)
			}
			if recorder.Code != http.StatusUnauthorized {
				return
			}
			if got := recorder.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("Expected WWW-Authenticate: Bearer, got %q", got)
			}
			var details APIResponseDetails
			if err := json.Unmarshal(recorder.Body.Bytes(), &details); err != nil || details.Code != UNIQUSH_ERROR_UNAUTHORIZED {
				t.Errorf("Expected a JSON body with code %s, got %q (%v)", UNIQUSH_ERROR_UNAUTHORIZED, recorder.Body, err)
			}
			if !strings.Contains(logged.String(), testCase.path) {
				t.Errorf("Expected the rejection to be logged, got %q", logged.String())
			}
			for _, secret := range []string{token, "guess-0"} {
				if strings.Contains(recorder.Body.String()+logged.String(), secret) {
					t.Errorf("A token appeared in the response or the log: %q %q", recorder.Body, logged.String())
				}
			}
		})
	}
}

func TestLoadAPIToken(t *testing.T) {
	dir := t.TempDir()
	write := func(name, contents string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	testCases := []struct {
		name      string
		tokenFile string
		want      string
		wantErr   bool
	}{
		{"not configured", "", "", false},
		{"trailing newline trimmed", write("newline", "abc\n"), "abc", false},
		{"trailing CRLF trimmed", write("crlf", "abc\r\n"), "abc", false},
		{"empty file refused", write("empty", "\n"), "", true},
		{"missing file refused", filepath.Join(dir, "missing"), "", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			c := conf.NewConfigFile()
			c.AddOption("WebFrontend", "token_file", testCase.tokenFile)
			got, err := LoadAPIToken(c, silentLoggers()[LoggerWeb])
			if (err != nil) != testCase.wantErr || got != testCase.want {
				t.Errorf("Expected %q (error: %v), got %q, %v", testCase.want, testCase.wantErr, got, err)
			}
		})
	}
}

func TestLoadSocketMode(t *testing.T) {
	testCases := []struct {
		value   string
		want    os.FileMode
		wantErr bool
	}{
		{"", 0o660, false},
		{"0600", 0o600, false},
		{"rw-rw----", 0, true},
		{"1777", 0, true},
	}
	for _, testCase := range testCases {
		c := conf.NewConfigFile()
		c.AddOption("WebFrontend", "socket_mode", testCase.value)
		got, err := LoadSocketMode(c)
		if (err != nil) != testCase.wantErr || got != testCase.want {
			t.Errorf("socket_mode=%q: expected %o (error: %v), got %o, %v", testCase.value, testCase.want, testCase.wantErr, got, err)
		}
	}
}

func unixClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
}

// TestUnixSocketServesAndIsRemovedOnStop drives Run, which registers on
// http.DefaultServeMux and so can only be called once per test binary.
func TestUnixSocketServesAndIsRemovedOnStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uniqush.sock")
	ln, err := listen("unix:"+path, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if fi, statErr := os.Stat(path); statErr != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("Expected the socket to have mode 0640, got %v, %v", fi.Mode(), statErr)
	}

	psm := push.GetPushServiceManager()
	api := NewRestAPI(psm, silentLoggers(), "uniqush-push test", NewPushBackEnd(psm, &healthDatabase{}, silentLoggers()))
	api.requireToken("t")
	stopChan := make(chan bool)
	go api.Run(ln, stopChan)

	request, _ := http.NewRequest(http.MethodGet, "http://uniqush"+VersionInfoURL, nil)
	request.Header.Set("Authorization", "Bearer t")
	response, err := unixClient(path).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 over the socket, got %d", response.StatusCode)
	}

	go api.stop(nil, "test")
	<-stopChan
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("Expected the socket file to be removed on stop, got %v", err)
	}
}

func TestListenUnixLeavesWhatIsNotAStaleSocket(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "stale.sock")
	staleListener, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	staleListener.(*net.UnixListener).SetUnlinkOnClose(false)
	staleListener.Close()

	live := filepath.Join(dir, "live.sock")
	liveListener, err := net.Listen("unix", live)
	if err != nil {
		t.Fatal(err)
	}
	defer liveListener.Close()

	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"a socket nothing listens on is replaced", stale, false},
		{"a socket in use is refused", live, true},
		{"a regular file is refused", regular, true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ln, err := listen("unix:"+testCase.path, 0o600)
			if ln != nil {
				ln.Close()
			}
			if (err != nil) != testCase.wantErr {
				t.Errorf("Expected an error: %v, got %v", testCase.wantErr, err)
			}
			if _, statErr := os.Lstat(testCase.path); testCase.wantErr && statErr != nil {
				t.Errorf("Expected %s to be left alone, got %v", testCase.path, statErr)
			}
		})
	}
}

package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/push"
)

func expectEquals(t *testing.T, expected interface{}, actual interface{}, msg string) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("expectEquals failed: %s: %#v != %#v", msg, expected, actual)
	}
}

func expectStringEquals(t *testing.T, expected string, actual string, msg string) {
	t.Helper()
	if expected != actual {
		t.Errorf("expectEquals failed: %s: %q != %q", msg, expected, actual)
	}
}

func TestOpenConfig(t *testing.T) {
	c, err := OpenConfig("conf/uniqush-push.conf")
	if err != nil {
		t.Fatalf("Unexpected error loading example config: %v", err)
	}
	fmt.Printf("c: %#v\n", c)
	getString := func(section, setting string) string {
		s, err := c.GetString(section, setting)
		if err != nil {
			t.Errorf("Failed to get section %q setting %q: %v", section, setting, err)
		}
		return s
	}

	expectStringEquals(t, "on", getString("WebFrontend", "log"), "unexpected data for log")
	expectStringEquals(t, "standard", getString("WebFrontend", "loglevel"), "unexpected data for loglevel")
	expectStringEquals(t, "localhost:9898", getString("WebFrontend", "addr"), "unexpected data for addr")

	dbConf, err := LoadDatabaseConfig(c)
	if err != nil {
		t.Fatalf("Failed to load database config section: %v", err)
	}
	expectedDbConf := &db.DatabaseConfig{
		Engine:             "redis",
		Name:               "0",
		Host:               "localhost",
		Port:               -1,  // redis db package converts this to default redis port
		EverySec:           600, // TODO: Change configparser.go to make this 60?
		LeastDirty:         10,
		CacheSize:          1024,
		PushServiceManager: push.GetPushServiceManager(),
	}
	expectEquals(t, *expectedDbConf, *dbConf, "expected config settings to be parsed")
}

func TestExtractLogLevel(t *testing.T) {
	expectLogLevelForName := func(level int, loglevel string) {
		actualLevel, warningMsg := extractLogLevel(loglevel)
		expectStringEquals(t, "", warningMsg, "expected no warning")
		expectEquals(t, level, actualLevel, "expected log level to be parsed")
	}
	expectLogLevelForName(log.LOGLEVEL_WARN, "warn")
	expectLogLevelForName(log.LOGLEVEL_ERROR, "error")
	expectLogLevelForName(log.LOGLEVEL_INFO, "standard")

	level, warningMsg := extractLogLevel("blue")
	expectStringEquals(t, `Unsupported loglevel "blue". Supported values: alert, error, warn/warning, standard/verbose/info, and debug`, warningMsg, "expected a warning message")
	expectEquals(t, log.LOGLEVEL_INFO, level, "expected INFO level fallback")
}

package main

import (
	"fmt"
	"testing"

	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/test_util"
)

func TestOpenConfig(t *testing.T) {
	c, err := OpenConfig("conf/uniqush-push.conf")
	if err != nil {
		t.Fatalf("Unexpected error loading example config: %v", err)
	}
	fmt.Printf("c: %#v\n", c)
	getString := func(section, setting string) string {
		s, loadErr := c.GetString(section, setting)
		if loadErr != nil {
			t.Errorf("Failed to get section %q setting %q: %v", section, setting, loadErr)
		}
		return s
	}

	test_util.ExpectStringEquals(t, "on", getString("WebFrontend", "log"), "unexpected data for log")
	test_util.ExpectStringEquals(t, "standard", getString("WebFrontend", "loglevel"), "unexpected data for loglevel")
	test_util.ExpectStringEquals(t, "localhost:9898", getString("WebFrontend", "addr"), "unexpected data for addr")

	dbConf, err := LoadDatabaseConfig(c)
	if err != nil {
		t.Fatalf("Failed to load database config section: %v", err)
	}
	expectedDbConf := &db.DatabaseConfig{
		Engine:             "redis",
		Name:               "0",
		Host:               "localhost",
		Port:               -1, // redis db package converts this to default redis port
		SlaveHost:          "",
		SlavePort:          -1,
		EverySec:           600, // TODO: Change configparser.go to make this 60?
		LeastDirty:         10,
		CacheSize:          1024,
		PushServiceManager: push.GetPushServiceManager(),
	}
	test_util.ExpectEquals(t, *expectedDbConf, *dbConf, "expected config settings to be parsed")
}

func TestExtractLogLevel(t *testing.T) {
	expectLogLevelForName := func(level int, loglevel string) {
		actualLevel, warningMsg := extractLogLevel(loglevel)
		test_util.ExpectStringEquals(t, "", warningMsg, "expected no warning")
		test_util.ExpectEquals(t, level, actualLevel, "expected log level to be parsed")
	}
	expectLogLevelForName(log.LOGLEVEL_WARN, "warn")
	expectLogLevelForName(log.LOGLEVEL_ERROR, "error")
	expectLogLevelForName(log.LOGLEVEL_INFO, "standard")

	level, warningMsg := extractLogLevel("blue")
	test_util.ExpectStringEquals(t, `Unsupported loglevel "blue". Supported values: alert, error, warn/warning, standard/verbose/info, and debug`, warningMsg, "expected a warning message")
	test_util.ExpectEquals(t, log.LOGLEVEL_INFO, level, "expected INFO level fallback")
}

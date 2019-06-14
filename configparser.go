/*
 * Copyright 2011 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/uniqush/goconf/conf"
	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/push"
)

// Logger* is an enum of the indices of the loggers for the various log categories.
const (
	LoggerWeb = iota
	LoggerAddPSP
	LoggerRemovePSP
	LoggerPSPs
	LoggerSub
	LoggerUnsub
	LoggerPush
	LoggerSubscriptions
	LoggerServices
	LoggerPreview
	NumberOfLoggers
)

func extractLogLevel(loglevel string) (int, string) {
	warningMsg := ""
	var level int
	switch strings.ToLower(loglevel) {
	case "alert":
		level = log.LOGLEVEL_ALERT
	case "error":
		level = log.LOGLEVEL_ERROR
	case "warn", "warning":
		level = log.LOGLEVEL_WARN
	case "standard", "verbose", "info":
		level = log.LOGLEVEL_INFO
	case "debug":
		level = log.LOGLEVEL_DEBUG
	default:
		warningMsg = fmt.Sprintf("Unsupported loglevel %q. Supported values: alert, error, warn/warning, standard/verbose/info, and debug", loglevel)
		level = log.LOGLEVEL_INFO
	}
	return level, warningMsg
}

func loadLogger(writer io.Writer, c *conf.ConfigFile, field string, prefix string) log.Logger {
	var loglevel string
	var logswitch bool
	var err error

	logswitch, err = c.GetBool(field, "log")
	if err != nil {
		logswitch = true
	}

	if writer == nil {
		writer = os.Stderr
	}

	loglevel, err = c.GetString(field, "loglevel")
	if err != nil {
		loglevel = "standard"
	}
	var level int
	warningMsg := ""

	if logswitch {
		level, warningMsg = extractLogLevel(loglevel)
	} else {
		level = log.LOGLEVEL_SILENT
	}

	logger := log.NewLogger(writer, prefix, level)
	if warningMsg != "" {
		logger.Warn(warningMsg)
	}
	return logger
}

// LoadDatabaseConfig returns a representation of the [Database] section from uniqush.conf
func LoadDatabaseConfig(cf *conf.ConfigFile) *db.DatabaseConfig {
	c := new(db.DatabaseConfig)

	getDbConfigString := func(key, defaultValue string) string {
		value, err := cf.GetString("Database", key)
		if err != nil || value == "" {
			return defaultValue
		}
		return value
	}
	var err error
	c.PushServiceManager = push.GetPushServiceManager()
	c.Engine = getDbConfigString("engine", "redis")
	c.Name = getDbConfigString("name", "0")
	c.Port, err = cf.GetInt("Database", "port")
	if err != nil || c.Port <= 0 {
		c.Port = -1
	}
	c.Host = getDbConfigString("host", "localhost")
	c.SlavePort, err = cf.GetInt("Database", "slave_port")
	if err != nil || c.SlavePort <= 0 {
		c.SlavePort = -1
	}
	c.SlaveHost = getDbConfigString("slave_host", "")
	c.Password = getDbConfigString("password", "")
	i, e := cf.GetInt("Database", "everysec")
	// TODO: Change condition to < 60 or change assignment to c.EverySec = 60?
	c.EverySec = int64(i)
	if e != nil || c.EverySec <= 60 {
		c.EverySec = 600
	}
	c.LeastDirty, err = cf.GetInt("Database", "leastdirty")
	if err != nil || c.LeastDirty < 0 {
		c.LeastDirty = 10
	}
	c.CacheSize, err = cf.GetInt("Database", "cachesize")
	if err != nil || c.CacheSize < 0 {
		c.CacheSize = 1024
	}

	return c
}

const (
	defaultConfigFilePath = "/etc/uniqush/uniqush.conf"
)

// OpenConfig opens the uniqush.conf file at filename, or returns an error
func OpenConfig(filename string) (c *conf.ConfigFile, err error) {
	if filename == "" {
		filename = defaultConfigFilePath
	}
	c, err = conf.ReadConfigFile(filename)
	if err != nil {
		return nil, err
	}
	return
}

// LoadLoggers will return an array of loggers, for each type in the enum.
// The log level of individual loggers vary based on the config.
func LoadLoggers(c *conf.ConfigFile) []log.Logger {
	var logfile io.Writer

	logfilename, err := c.GetString("default", "logfile")
	if err == nil && logfilename != "" {
		logfile, err = os.OpenFile(logfilename, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
		if err != nil {
			logfile = os.Stderr
			fmt.Fprintf(
				logfile,
				"Failed to open the configured logfile %q (err=%q). uniqush-push will write all logs to stderr instead...\n",
				logfilename,
				err,
			)
		}
	} else {
		logfile = os.Stderr
	}

	loggers := make([]log.Logger, NumberOfLoggers)

	loggerConfigs := map[int]string{
		LoggerWeb:           "WebFrontend",
		LoggerAddPSP:        "AddPushServiceProvider",
		LoggerRemovePSP:     "RemovePushServiceProvider",
		LoggerPSPs:          "PSPs",
		LoggerSub:           "Subscribe",
		LoggerUnsub:         "Unsubscribe",
		LoggerPush:          "Push",
		LoggerSubscriptions: "Subscriptions",
		LoggerServices:      "Services",
		LoggerPreview:       "Preview",
	}
	for loggerIndex, loggerName := range loggerConfigs {
		loggers[loggerIndex] = loadLogger(logfile, c, loggerName, fmt.Sprintf("[%s]", loggerName))
	}

	return loggers
}

// LoadRestAddr returns the address to listen to HTTP requests on, or returns an error.
// The default is localhost:9898, which will accept connections only from localhost.
// 0.0.0.0:9898 can be used to listen in on all interfaces, a firewall to control access to uniqush-push is strongly recommended.
func LoadRestAddr(c *conf.ConfigFile) (string, error) {
	addr, err := c.GetString("WebFrontend", "addr")
	if err != nil || addr == "" {
		addr = "localhost:9898"
		err = nil
	}
	return addr, err
}

// Run will load the configuration and start the uniqush-push server and REST API based on that config.
func Run(conf, version string) error {
	c, err := OpenConfig(conf)
	if err != nil {
		return err
	}
	loggers := LoadLoggers(c)
	dbconf := LoadDatabaseConfig(c)
	addr, err := LoadRestAddr(c)
	if err != nil {
		return err
	}
	psm := push.GetPushServiceManager()
	psm.SetConfigFile(c)

	db, err := db.NewPushDatabaseWithoutCache(dbconf)
	if err != nil {
		return err
	}

	backend := NewPushBackEnd(psm, db, loggers)
	rest := NewRestAPI(psm, loggers, version, backend)
	stopChan := make(chan bool)
	go rest.signalSetup()
	go rest.Run(addr, stopChan)
	<-stopChan
	return nil
}

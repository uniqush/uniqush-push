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
	"code.google.com/p/goconf/conf"
	. "github.com/uniqush/log"
	. "github.com/uniqush/uniqush-push/db"
	. "github.com/uniqush/uniqush-push/push"
	"io"
	"os"
	"strings"
)

const (
	LOGGER_WEB = iota
	LOGGER_ADDPSP
	LOGGER_RMPSP
	LOGGER_SUB
	LOGGER_UNSUB
	LOGGER_PUSH
	LOGGER_NR_LOGGERS
)

func loadLogger(writer io.Writer, c *conf.ConfigFile, field string, prefix string) (Logger, error) {
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

	if logswitch {
		switch strings.ToLower(loglevel) {
		case "standard":
			level = LOGLEVEL_INFO
		case "verbose":
			level = LOGLEVEL_INFO
		case "debug":
			level = LOGLEVEL_DEBUG
		default:
			level = LOGLEVEL_INFO
		}
	} else {
		level = LOGLEVEL_SILENT
	}

	logger := NewLogger(writer, prefix, level)
	return logger, nil
}

func LoadDatabaseConfig(cf *conf.ConfigFile) (*DatabaseConfig, error) {
	var err error
	c := new(DatabaseConfig)
	c.PushServiceManager = GetPushServiceManager()
	c.Engine, err = cf.GetString("Database", "engine")
	if err != nil || c.Engine == "" {
		c.Engine = "redis"
	}
	c.Name, err = cf.GetString("Database", "name")
	if err != nil || c.Name == "" {
		c.Name = "0"
	}
	c.Port, err = cf.GetInt("Database", "port")
	if err != nil || c.Port <= 0 {
		c.Port = -1
	}
	c.Host, err = cf.GetString("Database", "host")
	if err != nil || c.Host == "" {
		c.Host = "localhost"
	}
	c.Password, err = cf.GetString("Database", "password")
	if err != nil {
		c.Password = ""
	}
	i, e := cf.GetInt("Database", "everysec")
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

	return c, nil
}

var (
	defaultConfigFilePath string = "/etc/uniqush/uniqush.conf"
)

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

func LoadLoggers(c *conf.ConfigFile) (loggers []Logger, err error) {
	var logfile io.Writer

	logfilename, err := c.GetString("default", "logfile")
	if err == nil && logfilename != "" {
		logfile, err = os.OpenFile(logfilename, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
		if err != nil {
			logfile = os.Stderr
		}
	} else {
		logfile = os.Stderr
	}

	loggers = make([]Logger, LOGGER_NR_LOGGERS)
	loggers[LOGGER_WEB], err = loadLogger(logfile, c, "WebFrontend", "[WebFrontend]")
	if err != nil {
		loggers = nil
		return
	}

	loggers[LOGGER_ADDPSP], err = loadLogger(logfile, c, "AddPushServiceProvider", "[AddPushServiceProvider]")
	if err != nil {
		loggers = nil
		return
	}

	loggers[LOGGER_RMPSP], err = loadLogger(logfile, c, "RemovePushServiceProvider", "[RemovePushServiceProvider]")
	if err != nil {
		loggers = nil
		return
	}

	loggers[LOGGER_SUB], err = loadLogger(logfile, c, "Subscribe", "[Subscribe]")
	if err != nil {
		loggers = nil
		return
	}

	loggers[LOGGER_UNSUB], err = loadLogger(logfile, c, "Unsubscribe", "[Unsubscribe]")
	if err != nil {
		loggers = nil
		return
	}

	loggers[LOGGER_PUSH], err = loadLogger(logfile, c, "Push", "[Push]")
	if err != nil {
		loggers = nil
		return
	}
	return
}

func LoadRestAddr(c *conf.ConfigFile) (string, error) {
	addr, err := c.GetString("WebFrontend", "addr")
	if err != nil || addr == "" {
		addr = "localhost:9898"
		err = nil
	}
	return addr, err
}

func Run(conf, version string) error {
	c, err := OpenConfig(conf)
	if err != nil {
		return err
	}
	loggers, err := LoadLoggers(c)
	if err != nil {
		return err
	}
	dbconf, err := LoadDatabaseConfig(c)
	if err != nil {
		return err
	}
	addr, err := LoadRestAddr(c)
	if err != nil {
		return err
	}
	psm := GetPushServiceManager()

	db, err := NewPushDatabaseWithoutCache(dbconf)
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

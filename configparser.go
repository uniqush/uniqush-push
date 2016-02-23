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
	"github.com/orsonwang/uniqush-push/db"
	"github.com/orsonwang/uniqush-push/push"
	conf "github.com/pelletier/go-toml"
	"github.com/uniqush/log"
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

func loadLogger(writer io.Writer, c *conf.TomlTree, field string, prefix string) log.Logger {
	var loglevel string
	var logswitch bool

	logswitch = true
	logswitch = c.Get(field + "." + "log").(bool)

	if writer == nil {
		writer = os.Stderr
	}

	loglevel = c.Get(field + "." + "loglevel").(string)
	if loglevel == "" {
		loglevel = "standard"
	}
	var level int

	if logswitch {
		switch strings.ToLower(loglevel) {
		case "standard":
			level = log.LOGLEVEL_INFO
		case "verbose":
			level = log.LOGLEVEL_INFO
		case "debug":
			level = log.LOGLEVEL_DEBUG
		default:
			level = log.LOGLEVEL_INFO
		}
	} else {
		level = log.LOGLEVEL_SILENT
	}

	logger := log.NewLogger(writer, prefix, level)
	return logger
}

func LoadDatabaseConfig(cf *conf.TomlTree) *db.DatabaseConfig {
	c := new(db.DatabaseConfig)
	c.PushServiceManager = push.GetPushServiceManager()
	c.Engine = cf.Get("Database" + "." + "engine").(string)
	if c.Engine == "" {
		c.Engine = "redis"
	}
	c.Name = cf.Get("Database" + "." + "name").(string)
	if c.Name == "" {
		c.Name = "0"
	}
	c.Port = cf.Get("Database" + "." + "port").(int)
	if c.Port == 0 {
		c.Port = -1
	}
	c.Host = cf.Get("Database" + "." + "host").(string)
	if c.Host == "" {
		c.Host = "localhost"
	}
	c.Password = cf.Get("Database" + "." + "password").(string)

	i := cf.Get("Database" + "." + "everysec").(int)
	c.EverySec = int64(i)
	if c.EverySec <= 60 {
		c.EverySec = 600
	}
	c.LeastDirty = cf.Get("Database" + "." + "leastdirty").(int)
	if c.LeastDirty == 0 {
		c.LeastDirty = 10
	}
	c.CacheSize = cf.Get("Database" + "." + "cachesize").(int)
	if c.CacheSize == 0 {
		c.CacheSize = 1024
	}

	return c
}

var (
	defaultConfigFilePath string = "/etc/uniqush/uniqush.conf"
)

func OpenConfig(filename string) (c *conf.TomlTree, err error) {
	if filename == "" {
		filename = defaultConfigFilePath
	}
	c, err = conf.LoadFile(filename)
	if err != nil {
		return nil, err
	}
	return
}

func LoadLoggers(c *conf.TomlTree) (loggers []log.Logger, err error) {
	var logfile io.Writer

	logfilename := c.Get("default" + "." + "logfile").(string)
	if logfilename != "" {
		logfile, err = os.OpenFile(logfilename, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
		if err != nil {
			logfile = os.Stderr
		}
	} else {
		logfile = os.Stderr
	}

	loggers = make([]log.Logger, LOGGER_NR_LOGGERS)
	loggers[LOGGER_WEB] = loadLogger(logfile, c, "WebFrontend", "[WebFrontend]")
	loggers[LOGGER_ADDPSP] = loadLogger(logfile, c, "AddPushServiceProvider", "[AddPushServiceProvider]")
	loggers[LOGGER_RMPSP] = loadLogger(logfile, c, "RemovePushServiceProvider", "[RemovePushServiceProvider]")
	loggers[LOGGER_SUB] = loadLogger(logfile, c, "Subscribe", "[Subscribe]")
	loggers[LOGGER_UNSUB] = loadLogger(logfile, c, "Unsubscribe", "[Unsubscribe]")
	loggers[LOGGER_PUSH] = loadLogger(logfile, c, "Push", "[Push]")
	return
}

func LoadRestAddr(c *conf.TomlTree) string {
	addr := c.Get("WebFrontend" + "." + "addr").(string)
	if addr == "" {
		addr = "localhost:9898"
	}
	return addr
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

	dbconf := LoadDatabaseConfig(c)

	addr := LoadRestAddr(c)
	psm := push.GetPushServiceManager()

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

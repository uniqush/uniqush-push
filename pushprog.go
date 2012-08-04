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
	//"goconf.googlecode.com/hg"
	"code.google.com/p/monnand-goconf"
	"io"
	"os"
	"strings"
	. "github.com/uniqush/log"
	. "github.com/uniqush/pushdb"
	. "github.com/uniqush/pushsys"
)

func (s *PushProgram) loadLogInfo(c *conf.ConfigFile, field string, prefix string) (*Logger, error) {
	var loglevel string
	var logswitch bool
	var err error
	var writer io.Writer

	logswitch, err = c.GetBool(field, "log")
	if err != nil {
		logswitch = true
	}

	writer = s.logfile
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
		level = LOGLEVEL_FATAL
	}

	logger := NewLogger(writer, prefix, level)
	return logger, nil
}

func loadDatabaseConfig(cf *conf.ConfigFile) (*DatabaseConfig, error) {
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

type PushProgram struct {
	Frontend PushFrontEnd
	Backend  PushBackEnd
	Stopch   chan bool
	Bridge   chan *Request
	Database PushDatabase
	psm      *PushServiceManager
	logfile  io.WriteCloser
	version  string
}

var (
	defaultConfigFilePath string = "/etc/uniqush/uniqush.conf"
)

func (s *PushProgram) Finalize() {
	s.Frontend.Finalize()
	s.Backend.Finalize()
	s.logfile.Close()
	s.Database.FlushCache()
	s.psm.Finalize()
}

func LoadPushProgram(filename, version string) (*PushProgram, error) {
	if filename == "" {
		filename = defaultConfigFilePath
	}
	c, err := conf.ReadConfigFile(filename)
	if err != nil {
		return nil, err
	}

	ret := new(PushProgram)
	ret.Stopch = make(chan bool)
	ret.Bridge = make(chan *Request)
	ret.logfile = os.Stderr
	ret.version = version

	logfilename, err := c.GetString("default", "logfile")
	if err == nil && logfilename != "" {
		ret.logfile, err = os.OpenFile(logfilename,
			os.O_WRONLY|
				os.O_APPEND|
				os.O_CREATE, 0600)
		if err != nil {
			ret.logfile = os.Stderr
		}
	}

	logger, e10 := ret.loadLogInfo(c, "WebFrontend", "[WebFrontend]")
	if e10 != nil {
		return nil, e10
	}
	addr, e20 := c.GetString("WebFrontend", "addr")
	if e20 != nil || addr == "" {
		addr = "localhost:9898"
	}

	psm := GetPushServiceManager()
	ret.psm = psm

	ret.Frontend = newWebPushFrontEnd(ret.Bridge, logger, addr, psm, version)
	ret.Frontend.SetStopChannel(ret.Stopch)

	logger, e10 = ret.loadLogInfo(c, "Backend", "[Backend]")
	if e10 != nil {
		return nil, e10
	}
	ret.Backend = NewPushBackEnd(ret.Bridge, logger)

	var p RequestProcessor

	// Load Database
	dbconf, e0 := loadDatabaseConfig(c)
	if e0 != nil {
		return nil, e0
	}
	dbf, e30 := NewPushDatabaseWithoutCache(dbconf)

	if e30 != nil {
		return nil, e30
	}
	ret.Database = dbf

	// Load Processors
	logger, e10 = ret.loadLogInfo(c, "AddPushServiceProvider", "[AddPushServiceProvider]")
	if e10 != nil {
		return nil, e10
	}
	p = NewAddPushServiceProviderProcessor(logger, dbf)
	ret.Backend.SetProcessor(ACTION_ADD_PUSH_SERVICE_PROVIDER, p)

	logger, e10 = ret.loadLogInfo(c, "RemovePushServiceProvider", "[RemovePushServiceProvider]")
	if e10 != nil {
		return nil, e10
	}
	p = NewRemovePushServiceProviderProcessor(logger, dbf)
	ret.Backend.SetProcessor(ACTION_REMOVE_PUSH_SERVICE_PROVIDER, p)

	logger, e10 = ret.loadLogInfo(c, "Subscribe", "[Subscribe]")
	if e10 != nil {
		return nil, e10
	}
	p = NewSubscribeProcessor(logger, dbf)
	ret.Backend.SetProcessor(ACTION_SUBSCRIBE, p)

	logger, e10 = ret.loadLogInfo(c, "Unsubscribe", "[Unsubscribe]")
	if e10 != nil {
		return nil, e10
	}
	p = NewUnsubscribeProcessor(logger, dbf)
	ret.Backend.SetProcessor(ACTION_UNSUBSCRIBE, p)

	logger, e10 = ret.loadLogInfo(c, "Push", "[Push]")
	if e10 != nil {
		return nil, e10
	}
	p = NewPushProcessor(logger, dbf, ret.Bridge, psm)
	ret.Backend.SetProcessor(ACTION_PUSH, p)
	return ret, nil
}

func (s *PushProgram) Run() {
	defer s.Finalize()
	go s.signalSetup()
	go s.Frontend.Run()
	go s.Backend.Run()
	<-s.Stopch
}

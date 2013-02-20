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



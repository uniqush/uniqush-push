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

// Package log is uniqush's levelled logger.
//
// It was github.com/uniqush/log until it was adopted here. That repository had
// no go.mod, no releases to tag a fix into, and one user -- so a bug in it could
// not be fixed without first publishing a module for it. Being in-tree, it can
// be fixed in the commit that finds the bug, and it is covered by this project's
// linters and CI.
//
// Each level's writer is decided once, when the logger is built: a level at or
// below the configured one gets a real log.Logger, and everything above it gets
// a writer that discards. That is why a disabled Debugf costs nothing beyond the
// call -- there is no Sprintf behind it.
//
// Fatal and Fatalf are the exception. They print at every level including
// LevelSilent, because a process that is about to exit should say why even when
// the operator has turned logging off.
package log

import (
	"io"
	"log"
)

// Levels, in increasing order of detail. A logger built with one of these emits
// every level up to and including it.
//
// LevelSilent is below the rest: it emits nothing except Fatal, which always
// prints.
const (
	LevelSilent = -1
	LevelFatal  = 0
	LevelAlert  = 1
	LevelError  = 2
	LevelWarn   = 3
	LevelConfig = 4
	LevelInfo   = 5
	LevelDebug  = 6

	// numLevels is the number of real levels, i.e. everything but LevelSilent.
	numLevels = LevelDebug + 1
)

// levelNames are the tags that appear in a log line, indexed by level.
var levelNames = [numLevels]string{
	LevelFatal:  "[Fatal]",
	LevelAlert:  "[Alert]",
	LevelError:  "[Error]",
	LevelWarn:   "[Warning]",
	LevelConfig: "[Config]",
	LevelInfo:   "[Info]",
	LevelDebug:  "[Debug]",
}

// goLogger is the part of the standard library's log.Logger this package uses,
// so that a discarding implementation can stand in for it.
type goLogger interface {
	Print(v ...interface{})
	Printf(format string, v ...interface{})
	Fatal(v ...interface{})
	Fatalf(format string, v ...interface{})
}

var _ goLogger = &log.Logger{}
var _ goLogger = &nullLogger{}

// nullLogger discards Print and Printf without formatting them, which is what
// makes a disabled level cheap: reaching Sprintf would cost more than the call.
//
// Fatal and Fatalf still print. A level being disabled is a statement about how
// much detail the operator wants, not permission to exit silently.
type nullLogger struct {
	inner *log.Logger
}

func (l *nullLogger) Print(_ ...interface{})            {}
func (l *nullLogger) Printf(_ string, _ ...interface{}) {}

func (l *nullLogger) Fatal(v ...interface{}) {
	l.inner.Fatal(v...)
}

func (l *nullLogger) Fatalf(format string, v ...interface{}) {
	l.inner.Fatalf(format, v...)
}

func newNullLogger(writer io.Writer, prefix string, flag int) *nullLogger {
	return &nullLogger{inner: log.New(writer, prefix, flag)}
}

// Logger is a levelled logger. Every method behaves like its counterpart in the
// standard library's log package: the plain form takes values, the f form takes
// a format string.
type Logger interface {
	Debug(v ...interface{})
	Debugf(format string, v ...interface{})
	Info(v ...interface{})
	Infof(format string, v ...interface{})
	Config(v ...interface{})
	Configf(format string, v ...interface{})
	Warn(v ...interface{})
	Warnf(format string, v ...interface{})
	Error(v ...interface{})
	Errorf(format string, v ...interface{})
	Alert(v ...interface{})
	Alertf(format string, v ...interface{})
	Fatal(v ...interface{})
	Fatalf(format string, v ...interface{})
}

type logger struct {
	logLevel int
	loggers  [numLevels]goLogger
	prefix   string
	writer   io.Writer
}

var _ Logger = &logger{}

// nullWriter is where a logger built with a nil writer sends everything,
// including the levels that always print.
type nullWriter struct{}

func (f *nullWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

// NewLogger returns a logger that emits every level up to and including
// logLevel. A nil writer discards everything; LevelSilent discards everything
// except Fatal.
func NewLogger(writer io.Writer, prefix string, logLevel int) Logger {
	ret := new(logger)
	if writer == nil {
		ret.writer = &nullWriter{}
	} else {
		ret.writer = writer
	}
	ret.prefix = prefix
	ret.setLogLevel(logLevel)
	return ret
}

func (l *logger) setLogLevel(logLevel int) {
	if logLevel > LevelDebug {
		logLevel = LevelDebug
	}
	l.logLevel = logLevel
	for i := 0; i < numLevels; i++ {
		prefix := l.prefix + levelNames[i] + " "
		if i <= logLevel {
			l.loggers[i] = log.New(l.writer, prefix, log.LstdFlags)
		} else {
			l.loggers[i] = newNullLogger(l.writer, prefix, log.LstdFlags)
		}
	}
}

func (l *logger) Debug(v ...interface{}) { l.loggers[LevelDebug].Print(v...) }
func (l *logger) Info(v ...interface{})  { l.loggers[LevelInfo].Print(v...) }
func (l *logger) Config(v ...interface{}) {
	l.loggers[LevelConfig].Print(v...)
}
func (l *logger) Warn(v ...interface{})  { l.loggers[LevelWarn].Print(v...) }
func (l *logger) Error(v ...interface{}) { l.loggers[LevelError].Print(v...) }
func (l *logger) Alert(v ...interface{}) { l.loggers[LevelAlert].Print(v...) }
func (l *logger) Fatal(v ...interface{}) { l.loggers[LevelFatal].Fatal(v...) }

func (l *logger) Debugf(format string, v ...interface{}) {
	l.loggers[LevelDebug].Printf(format, v...)
}

func (l *logger) Infof(format string, v ...interface{}) {
	l.loggers[LevelInfo].Printf(format, v...)
}

func (l *logger) Configf(format string, v ...interface{}) {
	l.loggers[LevelConfig].Printf(format, v...)
}

func (l *logger) Warnf(format string, v ...interface{}) {
	l.loggers[LevelWarn].Printf(format, v...)
}

func (l *logger) Errorf(format string, v ...interface{}) {
	l.loggers[LevelError].Printf(format, v...)
}

func (l *logger) Alertf(format string, v ...interface{}) {
	l.loggers[LevelAlert].Printf(format, v...)
}

func (l *logger) Fatalf(format string, v ...interface{}) {
	l.loggers[LevelFatal].Fatalf(format, v...)
}

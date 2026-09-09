// Copyright (c) 2010-2012, Stephen Weinberg. All rights reserved.
// Use of this source code is governed by the BSD 3-clause licence in
// conf/LICENSE, which is not the licence covering the rest of uniqush-push.

// Package conf parses uniqush.conf: an INI-style configuration file of
// [section] headers and option=value lines.
//
// It was github.com/uniqush/goconf/conf until it was adopted here. That was a
// fork of ifwe/goconf, itself descended from a project abandoned in 2012, so
// there was no upstream left to send a fix to and uniqush-push was its only
// user. See conf/LICENSE for the original copyright, which the move does not
// change.
//
// Given a file:
//
//	[default]
//	host = example.com
//	port = 443
//
//	[apns]
//	pool_size = 13
//
// reading it looks like:
//
//	c, err := conf.ReadConfigFile("uniqush.conf")
//	c.GetString("default", "host") // "example.com"
//	c.GetInt("", "port")           // 443; "" means the default section
//	c.GetInt("apns", "port")       // 0 and a GetError
//
// The details that a configuration file's users end up depending on:
//
//   - Section and option names are case insensitive, and are lowercased on the
//     way in. Values are left exactly as written.
//   - A line beginning with #, ; or rem is a comment. A ; or # later in a line
//     starts a comment only if a space or tab precedes it, so a value may
//     contain either character.
//   - Either = or : separates an option from its value.
//   - A line beginning with the three letters rem is a comment, matched as a
//     prefix -- so an option named remove_stale is silently discarded. A trap,
//     left alone because requiring "rem " would turn a comment line beginning
//     "remember..." into a parse error and stop a config that used to work.
//   - A line with no separator continues the previous option's value, joined
//     with a newline.
//   - The last of several definitions of one option wins.
//
// Variable substitution -- goconf's %(name)s -- is not supported. It was
// removed upstream in 2012 and the documentation claiming otherwise outlived it
// by fourteen years.
package conf

import (
	"fmt"
	"strings"
)

// ConfigFile is the representation of configuration settings.
// The public interface is entirely through methods.
type ConfigFile struct { //nolint:revive // Renaming this is a breaking change for embedders, for a stutter in a name that reads fine at the call site.
	data map[string]map[string]string // Maps sections to options to values.
}

// Reasons carried by GetError and ReadError.
const (
	// SectionNotFound means the requested section is not in the file.
	SectionNotFound = iota
	// OptionNotFound means the section exists but does not define the option.
	OptionNotFound
	// BlankSection means a value appeared before any [section] header.
	BlankSection
	// CouldNotParse means a line, or a value being converted, was malformed.
	CouldNotParse
)

var (
	// DefaultSection is where options that precede any [section] header go, and
	// what an empty section name refers to. It must be lower case.
	DefaultSection = "default"

	// BoolStrings are the values GetBool accepts, compared case insensitively.
	BoolStrings = map[string]bool{
		"t":     true,
		"true":  true,
		"y":     true,
		"yes":   true,
		"on":    true,
		"1":     true,
		"f":     false,
		"false": false,
		"n":     false,
		"no":    false,
		"off":   false,
		"0":     false,
	}
)

// NewConfigFile creates an empty configuration representation, which can be
// filled with AddSection and AddOption.
func NewConfigFile() *ConfigFile {
	c := new(ConfigFile)
	c.data = make(map[string]map[string]string)

	c.AddSection(DefaultSection) // default section always exists

	return c
}

// AddSection adds a new section to the configuration.
// It returns true if the new section was inserted, and false if the section already existed.
func (c *ConfigFile) AddSection(section string) bool {
	section = strings.ToLower(section)

	if _, ok := c.data[section]; ok {
		return false
	}
	c.data[section] = make(map[string]string)

	return true
}

// RemoveSection removes a section from the configuration.
// It returns true if the section was removed, and false if section did not exist.
func (c *ConfigFile) RemoveSection(section string) bool {
	section = strings.ToLower(section)

	if _, ok := c.data[section]; !ok {
		return false
	}
	if section == DefaultSection {
		return false // default section cannot be removed
	}
	delete(c.data, section)

	return true
}

// AddOption adds a new option and value to the configuration.
// It returns true if the option and value were inserted, and false if the value was overwritten.
// If the section does not exist in advance, it is created.
func (c *ConfigFile) AddOption(section string, option string, value string) bool {
	c.AddSection(section) // make sure section exists

	section = strings.ToLower(section)
	option = strings.ToLower(option)

	_, ok := c.data[section][option]
	c.data[section][option] = value

	return !ok
}

// RemoveOption removes a option and value from the configuration.
// It returns true if the option and value were removed, and false otherwise,
// including if the section did not exist.
func (c *ConfigFile) RemoveOption(section string, option string) bool {
	section = strings.ToLower(section)
	option = strings.ToLower(option)

	if _, ok := c.data[section]; !ok {
		return false
	}

	_, ok := c.data[section][option]
	delete(c.data[section], option)

	return ok
}

// GetError is returned by the accessors when a section or option is missing, or
// a value will not convert to the requested type.
type GetError struct {
	Reason    int
	ValueType string
	Value     string
	Section   string
	Option    string
}

func (err GetError) Error() string {
	switch err.Reason {
	case SectionNotFound:
		return fmt.Sprintf("section '%s' not found", err.Section)
	case OptionNotFound:
		return fmt.Sprintf("option '%s' not found in section '%s'", err.Option, err.Section)
	case CouldNotParse:
		return fmt.Sprintf("could not parse %s value '%s'", err.ValueType, err.Value)
	}

	return "invalid get error"
}

// ReadError is returned when a line of the configuration file cannot be parsed.
type ReadError struct {
	Reason int
	Line   string
}

func (err ReadError) Error() string {
	switch err.Reason {
	case BlankSection:
		return "empty section name not allowed"
	case CouldNotParse:
		return fmt.Sprintf("could not parse line: %s", err.Line)
	}

	return "invalid read error"
}

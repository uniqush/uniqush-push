// Copyright (c) 2010-2012, Stephen Weinberg. All rights reserved.
// Use of this source code is governed by the BSD 3-clause licence in
// conf/LICENSE, which is not the licence covering the rest of uniqush-push.

package conf

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
)

// ReadConfigFile reads a file and returns a new configuration representation.
// This representation can be queried with GetString, etc.
func ReadConfigFile(fname string) (c *ConfigFile, err error) {
	var file *os.File

	if file, err = os.Open(fname); err != nil {
		return nil, err
	}

	c = NewConfigFile()
	if err = c.Read(file); err != nil {
		return nil, err
	}

	if err = file.Close(); err != nil {
		return nil, err
	}

	return c, nil
}

// ReadConfigBytes reads a configuration file that is already in memory.
func ReadConfigBytes(conf []byte) (c *ConfigFile, err error) {
	buf := bytes.NewBuffer(conf)

	c = NewConfigFile()
	if err = c.Read(buf); err != nil {
		return nil, err
	}

	return c, err
}

// Read reads an io.Reader and returns a configuration representation. This
// representation can be queried with GetString, etc.
func (c *ConfigFile) Read(reader io.Reader) (err error) {
	buf := bufio.NewReader(reader)

	var section, option string
	section = DefaultSection
	for {
		l, buferr := buf.ReadString('\n') // parse line-by-line
		l = strings.TrimSpace(l)

		if buferr != nil {
			// Anything other than the end of the input is a failure to read the
			// configuration, not the end of it. Returning nil here -- which this
			// did -- reported a half-read file as a complete one, so a truncated
			// config would start uniqush with whatever had been parsed so far.
			if !errors.Is(buferr, io.EOF) {
				return buferr
			}

			if len(l) == 0 {
				break
			}
		}

		// switch written for readability (not performance)
		switch {
		case len(l) == 0: // empty line
			continue

		case l[0] == '#': // comment
			continue

		case l[0] == ';': // comment
			continue

		case len(l) >= 3 && strings.ToLower(l[0:3]) == "rem": // comment (for windows users)
			continue

		case l[0] == '[' && l[len(l)-1] == ']': // new section
			option = "" // reset multi-line value
			section = strings.TrimSpace(l[1 : len(l)-1])
			c.AddSection(section)

		case section == "": // not new section and no section defined so far
			return ReadError{BlankSection, l}

		default: // other alternatives
			switch i := strings.IndexAny(l, "=:"); {
			case i > 0: // option and value
				option = strings.TrimSpace(l[0:i])
				value := strings.TrimSpace(stripComments(l[i+1:]))
				c.AddOption(section, option, value)

			case section != "" && option != "": // continuation of multi-line value
				prev, _ := c.GetRawString(section, option)
				value := strings.TrimSpace(stripComments(l))
				c.AddOption(section, option, prev+"\n"+value)

			default:
				return ReadError{CouldNotParse, l}
			}
		}

		// Reached end of file
		if errors.Is(buferr, io.EOF) {
			break
		}
	}
	return nil
}

// stripComments removes a trailing comment from a value. A ; or # only starts
// one when a space or tab precedes it, so a value may contain either character.
func stripComments(l string) string {
	for _, c := range []string{" ;", "\t;", " #", "\t#"} {
		if i := strings.Index(l, c); i != -1 {
			l = l[0:i]
		}
	}
	return l
}

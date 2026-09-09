// Copyright (c) 2010-2012, Stephen Weinberg. All rights reserved.
// Use of this source code is governed by the BSD 3-clause licence in
// conf/LICENSE, which is not the licence covering the rest of uniqush-push.

package conf

import (
	"strconv"
	"strings"
)

// GetSections returns the list of sections in the configuration.
// (The default section always exists.)
func (c *ConfigFile) GetSections() (sections []string) {
	sections = make([]string, 0, len(c.data))

	for s := range c.data {
		sections = append(sections, s)
	}

	return sections
}

// HasSection checks if the configuration has the given section.
// (The default section always exists.)
func (c *ConfigFile) HasSection(section string) bool {
	if section == "" {
		section = DefaultSection
	}
	_, ok := c.data[strings.ToLower(section)]

	return ok
}

// GetOptions returns the list of options defined in the given section.
// It returns an error if the section does not exist and an empty list if the
// section is empty.
//
// Only that section's options. An option in the default section is not
// inherited by the others -- see HasOption.
func (c *ConfigFile) GetOptions(section string) (options []string, err error) {
	if section == "" {
		section = DefaultSection
	}
	section = strings.ToLower(section)

	if _, ok := c.data[section]; !ok {
		return nil, GetError{SectionNotFound, "", "", section, ""}
	}

	options = make([]string, 0, len(c.data[section]))
	for s := range c.data[section] {
		options = append(options, s)
	}

	return options, nil
}

// HasOption checks if the configuration has the given option in the section.
// It returns false if either the option or section do not exist.
//
// The default section is not consulted. It used to be, by this function and by
// GetOptions but by none of the accessors, so HasOption would answer true for an
// option GetString then reported as missing. Nothing in uniqush called either
// one, so they were brought in line with the accessors rather than the other way
// round: inheriting the default section would have changed what every existing
// uniqush.conf means.
func (c *ConfigFile) HasOption(section string, option string) bool {
	if section == "" {
		section = DefaultSection
	}
	section = strings.ToLower(section)
	option = strings.ToLower(option)

	if _, ok := c.data[section]; !ok {
		return false
	}

	_, ok := c.data[section][option]

	return ok
}

// GetRawString gets the string value for the given option in the section.
//
// It is the same as GetString: the two differed when values could refer to one
// another, and nothing unfolds a value now.
func (c *ConfigFile) GetRawString(section string, option string) (value string, err error) {
	if section == "" {
		section = DefaultSection
	}

	section = strings.ToLower(section)
	option = strings.ToLower(option)

	if _, ok := c.data[section]; ok {
		if value, ok = c.data[section][option]; ok {
			return value, nil
		}
		return "", GetError{OptionNotFound, "", "", section, option}
	}
	return "", GetError{SectionNotFound, "", "", section, option}
}

// GetString gets the string value for the given option in the section.
// It returns an error if either the section or the option do not exist.
func (c *ConfigFile) GetString(section string, option string) (value string, err error) {
	return c.GetRawString(section, option)
}

// GetInt has the same behaviour as GetString but converts the response to int.
func (c *ConfigFile) GetInt(section string, option string) (value int, err error) {
	sv, err := c.GetString(section, option)
	if err == nil {
		value, err = strconv.Atoi(sv)
		if err != nil {
			err = GetError{CouldNotParse, "int", sv, section, option}
		}
	}

	return value, err
}

// GetFloat64 has the same behaviour as GetString but converts the response to float64.
func (c *ConfigFile) GetFloat64(section string, option string) (value float64, err error) {
	sv, err := c.GetString(section, option)
	if err == nil {
		value, err = strconv.ParseFloat(sv, 64)
		if err != nil {
			err = GetError{CouldNotParse, "float64", sv, section, option}
		}
	}

	return value, err
}

// GetBool has the same behaviour as GetString but converts the response to bool.
// See BoolStrings for the values it accepts.
func (c *ConfigFile) GetBool(section string, option string) (value bool, err error) {
	sv, err := c.GetString(section, option)
	if err != nil {
		return false, err
	}

	value, ok := BoolStrings[strings.ToLower(sv)]
	if !ok {
		return false, GetError{CouldNotParse, "bool", sv, section, option}
	}

	return value, nil
}

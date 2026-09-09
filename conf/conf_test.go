// Copyright (c) 2010-2012, Stephen Weinberg. All rights reserved.
// Use of this source code is governed by the BSD 3-clause licence in
// conf/LICENSE, which is not the licence covering the rest of uniqush-push.

package conf

import (
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
)

const sampleConfig = `
[default]
host = example.com
port = 43
compression = on
active = false

[service-1]
port = 443
`

func readOrFail(t *testing.T, config string) *ConfigFile {
	t.Helper()

	c, err := ReadConfigBytes([]byte(config))
	if err != nil {
		t.Fatalf("Could not read the configuration: %v", err)
	}
	return c
}

func TestGetString(t *testing.T) {
	c := readOrFail(t, sampleConfig)

	testCases := []struct {
		section, option, expected string
	}{
		// An empty section name means the default section.
		{"", "host", "example.com"},
		{"default", "host", "example.com"},
	}

	for _, testCase := range testCases {
		value, err := c.GetString(testCase.section, testCase.option)
		if err != nil {
			t.Errorf("GetString(%q, %q) returned %v", testCase.section, testCase.option, err)
		} else if value != testCase.expected {
			t.Errorf("GetString(%q, %q) = %q, want %q",
				testCase.section, testCase.option, value, testCase.expected)
		}
	}
}

func TestGetInt(t *testing.T) {
	c := readOrFail(t, sampleConfig)

	if value, err := c.GetInt("default", "port"); err != nil || value != 43 {
		t.Errorf("GetInt(default, port) = %d, %v; want 43", value, err)
	}
	if value, err := c.GetInt("service-1", "port"); err != nil || value != 443 {
		t.Errorf("GetInt(service-1, port) = %d, %v; want 443", value, err)
	}
	if _, err := c.GetInt("default", "host"); err == nil {
		t.Error("Expected an error converting a non-numeric value to int")
	}
}

func TestGetBool(t *testing.T) {
	c := readOrFail(t, sampleConfig)

	if value, err := c.GetBool("default", "compression"); err != nil || !value {
		t.Errorf(`GetBool(default, compression) = %v, %v; want true ("on")`, value, err)
	}
	if value, err := c.GetBool("default", "active"); err != nil || value {
		t.Errorf("GetBool(default, active) = %v, %v; want false", value, err)
	}
	if _, err := c.GetBool("default", "host"); err == nil {
		t.Error("Expected an error converting a non-boolean value to bool")
	}
}

func TestGetBoolAcceptsEveryDocumentedSpelling(t *testing.T) {
	trueValues := []string{"t", "true", "TRUE", "y", "yes", "on", "On", "1"}
	falseValues := []string{"f", "false", "FALSE", "n", "no", "off", "Off", "0"}

	for _, value := range trueValues {
		c := readOrFail(t, "[s]\nk = "+value)
		if got, err := c.GetBool("s", "k"); err != nil || !got {
			t.Errorf("GetBool of %q = %v, %v; want true", value, got, err)
		}
	}
	for _, value := range falseValues {
		c := readOrFail(t, "[s]\nk = "+value)
		if got, err := c.GetBool("s", "k"); err != nil || got {
			t.Errorf("GetBool of %q = %v, %v; want false", value, got, err)
		}
	}
}

func TestMissingSectionAndOptionAreDistinguishable(t *testing.T) {
	c := readOrFail(t, sampleConfig)

	_, err := c.GetString("nosuchsection", "host")
	var getErr GetError
	if !errors.As(err, &getErr) || getErr.Reason != SectionNotFound {
		t.Errorf("Expected a SectionNotFound GetError, got %v", err)
	}

	_, err = c.GetString("service-1", "nosuchoption")
	if !errors.As(err, &getErr) || getErr.Reason != OptionNotFound {
		t.Errorf("Expected an OptionNotFound GetError, got %v", err)
	}
}

func TestNamesAreCaseInsensitiveAndValuesAreNot(t *testing.T) {
	c := readOrFail(t, "[SectionName]\nOptionName = MixedCaseValue")

	value, err := c.GetString("sectionname", "optionname")
	if err != nil {
		t.Fatalf("Expected the lowercased names to resolve, got %v", err)
	}
	if value != "MixedCaseValue" {
		t.Errorf("Expected the value to keep its case, got %q", value)
	}
	if !c.HasOption("SECTIONNAME", "OPTIONNAME") {
		t.Error("Expected HasOption to be case insensitive")
	}
}

func TestComments(t *testing.T) {
	c := readOrFail(t, strings.Join([]string{
		"[s]",
		"# hash comment",
		"; semicolon comment",
		"rem windows comment",
		"kept = value ; trailing comment",
		"also_kept = value\t# tabbed comment",
		"hash_in_value = pass#word",
	}, "\n"))

	testCases := []struct{ option, expected string }{
		{"kept", "value"},
		{"also_kept", "value"},
		// A # only starts a comment when a space or tab precedes it, so this is
		// a password, not a truncated one.
		{"hash_in_value", "pass#word"},
	}
	for _, testCase := range testCases {
		if value, err := c.GetString("s", testCase.option); err != nil || value != testCase.expected {
			t.Errorf("GetString(s, %s) = %q, %v; want %q", testCase.option, value, err, testCase.expected)
		}
	}
}

// TestAnOptionBeginningWithRemIsTreatedAsAComment records a trap rather than
// endorsing it. "rem" is the DOS comment keyword, and it is matched as a prefix,
// so an option named remove_stale is silently discarded.
//
// It is left alone deliberately: requiring "rem " would turn prose comments that
// existing configuration files begin with "remember..." into parse errors, and a
// config that used to start uniqush would stop.
func TestAnOptionBeginningWithRemIsTreatedAsAComment(t *testing.T) {
	c := readOrFail(t, "[s]\nremove_stale = 1")

	if c.HasOption("s", "remove_stale") {
		t.Error("Expected an option beginning with rem to be skipped as a comment")
	}
}

func TestAValueMayContinueOnTheNextLine(t *testing.T) {
	c := readOrFail(t, "[s]\nk = first\n  second\n")

	value, err := c.GetString("s", "k")
	if err != nil {
		t.Fatalf("GetString returned %v", err)
	}
	if want := "first\nsecond"; value != want {
		t.Errorf("Expected the continuation joined with a newline (%q), got %q", want, value)
	}
}

func TestTheLastDefinitionOfAnOptionWins(t *testing.T) {
	c := readOrFail(t, "[s]\nk = first\nk = second\n")

	if value, _ := c.GetString("s", "k"); value != "second" {
		t.Errorf("Expected the later definition to win, got %q", value)
	}
}

// failingReader returns some content and then an error that is not io.EOF, the
// way a real read can fail part way through a file.
type failingReader struct {
	content string
	read    bool
}

var errRead = errors.New("device failed")

func (r *failingReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, errRead
	}
	r.read = true
	return copy(p, r.content), nil
}

// TestAFailedReadIsReported is the regression test for a swallowed error. Read
// returned the nil named return value rather than the read error, so a
// configuration file that failed to read part way through was reported as
// complete, and uniqush would start with only the options that happened to
// arrive before the failure.
func TestAFailedReadIsReported(t *testing.T) {
	c := NewConfigFile()

	err := c.Read(&failingReader{content: "[s]\nk = v\n"})

	if err == nil {
		t.Fatal("Expected a read error to be reported, got nil")
	}
	if !errors.Is(err, errRead) {
		t.Errorf("Expected the underlying read error, got %v", err)
	}
}

func TestEndOfInputWithoutATrailingNewlineIsNotAnError(t *testing.T) {
	c := readOrFail(t, "[s]\nk = v")

	if value, err := c.GetString("s", "k"); err != nil || value != "v" {
		t.Errorf("Expected the last line to be parsed, got %q, %v", value, err)
	}
}

func TestReadReportsAnUnparseableLine(t *testing.T) {
	_, err := ReadConfigBytes([]byte("[s]\nthis line has no separator\n"))

	var readErr ReadError
	if !errors.As(err, &readErr) || readErr.Reason != CouldNotParse {
		t.Errorf("Expected a CouldNotParse ReadError, got %v", err)
	}
}

func TestSectionsAndOptions(t *testing.T) {
	c := readOrFail(t, sampleConfig)

	sections := c.GetSections()
	sort.Strings(sections)
	if want := []string{"default", "service-1"}; !equal(sections, want) {
		t.Errorf("GetSections() = %v, want %v", sections, want)
	}

	if !c.HasSection("service-1") || c.HasSection("nosuchsection") {
		t.Error("HasSection disagreed with the file")
	}

	options, err := c.GetOptions("service-1")
	if err != nil {
		t.Fatalf("GetOptions returned %v", err)
	}
	if want := []string{"port"}; !equal(options, want) {
		t.Errorf("GetOptions(service-1) = %v, want %v", options, want)
	}

	if _, err := c.GetOptions("nosuchsection"); err == nil {
		t.Error("Expected GetOptions to report a missing section")
	}
}

// TestTheDefaultSectionIsNotInherited pins the one place this package used to
// contradict itself: HasOption and GetOptions consulted the default section
// while every accessor ignored it, so HasOption reported an option that
// GetString could not return.
func TestTheDefaultSectionIsNotInherited(t *testing.T) {
	c := readOrFail(t, sampleConfig)

	if _, err := c.GetString("service-1", "host"); err == nil {
		t.Error("Expected a default-section option to be invisible from another section")
	}
	if c.HasOption("service-1", "host") {
		t.Error("Expected HasOption to agree with GetString about inheritance")
	}
}

func TestAddAndRemove(t *testing.T) {
	c := NewConfigFile()

	if !c.AddSection("s") {
		t.Error("Expected AddSection to report a new section")
	}
	if c.AddSection("s") {
		t.Error("Expected AddSection to report an existing section")
	}
	if !c.AddOption("s", "k", "v") {
		t.Error("Expected AddOption to report a new option")
	}
	if c.AddOption("s", "k", "v2") {
		t.Error("Expected AddOption to report an overwrite")
	}
	if value, _ := c.GetString("s", "k"); value != "v2" {
		t.Errorf("Expected the overwritten value, got %q", value)
	}
	if !c.RemoveOption("s", "k") || c.RemoveOption("s", "k") {
		t.Error("RemoveOption did not report what it did")
	}
	if !c.RemoveSection("s") || c.RemoveSection("s") {
		t.Error("RemoveSection did not report what it did")
	}
	if c.RemoveSection(DefaultSection) {
		t.Error("Expected the default section to be undeletable")
	}
	if !c.HasSection(DefaultSection) {
		t.Error("Expected the default section to survive")
	}
}

func TestAValueBeforeAnySectionGoesToTheDefaultSection(t *testing.T) {
	c := readOrFail(t, "k = v\n")

	if value, err := c.GetString(DefaultSection, "k"); err != nil || value != "v" {
		t.Errorf("Expected the default section to hold the value, got %q, %v", value, err)
	}
}

func TestReadConfigFileReportsAMissingFile(t *testing.T) {
	if _, err := ReadConfigFile("nosuchfile.conf"); err == nil {
		t.Error("Expected an error reading a file that does not exist")
	} else if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "nosuchfile.conf") {
		t.Errorf("Expected the error to name the file, got %v", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

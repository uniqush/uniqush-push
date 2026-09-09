package log

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// helperEnv names which fatal helper a subprocess should run. Fatal and Fatalf
// end in os.Exit, so they cannot be observed in the process running the test.
const helperEnv = "UNIQUSH_LOG_FATAL_HELPER"

// runFatalHelper re-runs this test binary with only the named helper enabled and
// returns what it printed, along with whether it exited non-zero.
func runFatalHelper(t *testing.T, helper string) (output string, exited bool) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=TestFatalHelpers", "-test.v")
	cmd.Env = append(os.Environ(), helperEnv+"="+helper)
	combined, err := cmd.CombinedOutput()
	return string(combined), err != nil
}

// TestFatalHelpers is the subprocess side of the fatal tests. It does nothing
// unless the parent asked for a specific helper.
func TestFatalHelpers(t *testing.T) {
	switch os.Getenv(helperEnv) {
	case "fatalf":
		NewLogger(os.Stdout, "[silenced] ", LevelSilent).
			Fatalf("cannot start: %v on port %d", "bind failed", 8080)
	case "fatal":
		NewLogger(os.Stdout, "[silenced] ", LevelSilent).
			Fatal("cannot start: ", "bind failed", " on port 8080")
	default:
		t.Skip("not a subprocess run")
	}
}

// TestFatalfFormatsItsArgumentsWhenSilenced is the regression test for the bug
// this package was adopted with. github.com/uniqush/log passed the variadic
// slice to the standard library as a single argument:
//
//	l.inner.Fatalf(format, v)
//
// so "cannot start: %v on port %d" came out as
// "cannot start: [bind failed 8080] on port %!d(MISSING)". A silenced logger is
// exactly where that mattered: it is reachable from log=off in uniqush.conf, and
// the fatal line is then the only thing an operator gets when uniqush dies.
func TestFatalfFormatsItsArgumentsWhenSilenced(t *testing.T) {
	output, exited := runFatalHelper(t, "fatalf")

	if want := "cannot start: bind failed on port 8080"; !strings.Contains(output, want) {
		t.Errorf("Expected the fatal line to contain %q, got:\n%s", want, output)
	}
	for _, unwanted := range []string{"%!d(MISSING)", "[bind failed 8080]"} {
		if strings.Contains(output, unwanted) {
			t.Errorf("Fatalf did not expand its arguments -- found %q in:\n%s", unwanted, output)
		}
	}
	if !exited {
		t.Error("Expected Fatalf to exit non-zero even at LevelSilent")
	}
	if !strings.Contains(output, "[Fatal]") {
		t.Errorf("Expected the [Fatal] tag on the line, got:\n%s", output)
	}
}

// TestFatalPrintsItsArgumentsWhenSilenced is the same bug in the non-format
// method, where the slice was rendered as "[a b c]" instead of its elements.
func TestFatalPrintsItsArgumentsWhenSilenced(t *testing.T) {
	output, exited := runFatalHelper(t, "fatal")

	if want := "cannot start: bind failed on port 8080"; !strings.Contains(output, want) {
		t.Errorf("Expected the fatal line to contain %q, got:\n%s", want, output)
	}
	if !exited {
		t.Error("Expected Fatal to exit non-zero even at LevelSilent")
	}
}

// emitAllLevels calls every non-fatal method, so a test can see which ones the
// configured level let through.
func emitAllLevels(l Logger) {
	l.Debug("debug")
	l.Info("info")
	l.Config("config")
	l.Warn("warn")
	l.Error("error")
	l.Alert("alert")
}

// containsLineWith reports whether some line carries both the level tag and the
// message. They are not adjacent in the output -- log.Logger puts its timestamp
// between them -- so this is checked per line rather than as one substring.
func containsLineWith(output, tag, message string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, tag) && strings.Contains(line, message) {
			return true
		}
	}
	return false
}

func TestALevelEmitsItselfAndEverythingMoreSevere(t *testing.T) {
	type line struct{ tag, message string }

	testCases := []struct {
		name    string
		level   int
		emitted []line
		silent  []string
	}{
		{
			name:  "warn",
			level: LevelWarn,
			// Alert and Error are more severe than the configured Warn, so they
			// are emitted too; Config and below are not.
			emitted: []line{{"[Warning]", "warn"}, {"[Error]", "error"}, {"[Alert]", "alert"}},
			silent:  []string{"config", "info", "debug"},
		},
		{
			name:  "debug emits everything",
			level: LevelDebug,
			emitted: []line{
				{"[Debug]", "debug"}, {"[Info]", "info"}, {"[Config]", "config"},
				{"[Warning]", "warn"}, {"[Error]", "error"}, {"[Alert]", "alert"},
			},
		},
		{
			name:   "silent emits nothing",
			level:  LevelSilent,
			silent: []string{"debug", "info", "config", "warn", "error", "alert"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var buffer bytes.Buffer
			emitAllLevels(NewLogger(&buffer, "[test] ", testCase.level))

			written := buffer.String()
			for _, want := range testCase.emitted {
				if !containsLineWith(written, want.tag, want.message) {
					t.Errorf("Expected a %s line carrying %q, got:\n%s", want.tag, want.message, written)
				}
			}
			for _, unwanted := range testCase.silent {
				if strings.Contains(written, unwanted) {
					t.Errorf("Expected no %q in the output, got:\n%s", unwanted, written)
				}
			}
		})
	}
}

// TestAboveDebugIsClampedToDebug covers a caller that passes a level past the
// end of the table: the loggers are indexed by level, so an unclamped value
// would panic rather than log.
func TestAboveDebugIsClampedToDebug(t *testing.T) {
	var buffer bytes.Buffer
	logger := NewLogger(&buffer, "", LevelDebug+100)

	logger.Debug("debug")

	if !strings.Contains(buffer.String(), "debug") {
		t.Errorf("Expected a level above LevelDebug to behave as LevelDebug, got:\n%s", buffer.String())
	}
}

func TestNilWriterDiscardsEverything(t *testing.T) {
	logger := NewLogger(nil, "[test] ", LevelDebug)
	if logger == nil {
		t.Fatal("Expected a logger for a nil writer, got nil")
	}

	// There is no output to inspect. What is being checked is that a nil writer
	// -- a documented way to silence a logger -- discards rather than panicking
	// on a nil dereference at the first call.
	emitAllLevels(logger)
}

func TestPrefixPrecedesTheLevelTag(t *testing.T) {
	var buffer bytes.Buffer
	NewLogger(&buffer, "[uniqush] ", LevelInfo).Info("hello")

	if want := "[uniqush] [Info] "; !strings.Contains(buffer.String(), want) {
		t.Errorf("Expected %q in the output, got:\n%s", want, buffer.String())
	}
}

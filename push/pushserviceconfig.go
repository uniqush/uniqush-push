package push

import (
	"errors"
	"time"

	"github.com/uniqush/uniqush-push/conf"
)

// errNoConfig is what every accessor returns when uniqush was started without a
// configuration file, so there is no section to read anything from.
//
// Lowercase, per staticcheck ST1005: an error string is usually wrapped into a
// longer sentence, and a capital in the middle of one reads as a mistake.
var errNoConfig = errors.New("no config")

// PushServiceConfig accesses the section for 'name' of the given ConfigFile.
type PushServiceConfig struct { //nolint:revive
	c    *conf.ConfigFile
	name string
}

// NewPushServiceConfig returns an accessor for the given section name of the unserialized config file (for the push service with that name, e.g. "apns").
func NewPushServiceConfig(c *conf.ConfigFile, name string) *PushServiceConfig {
	return &PushServiceConfig{
		c:    c,
		name: name,
	}
}

// GetString will return a string for the given option from this push service's section of the configuration file.
func (config *PushServiceConfig) GetString(option string) (string, error) {
	if config.c == nil {
		return "", errNoConfig
	}
	return config.c.GetString(config.name, option)
}

// GetInt will return an integer for the given option from this push service's section of the configuration file.
func (config *PushServiceConfig) GetInt(option string) (int, error) {
	if config.c == nil {
		return 0, errNoConfig
	}
	return config.c.GetInt(config.name, option)
}

// GetBool will return a boolean for the given option from this push service's section of the configuration file.
func (config *PushServiceConfig) GetBool(option string) (bool, error) {
	if config.c == nil {
		return false, errNoConfig
	}
	return config.c.GetBool(config.name, option)
}

// GetSeconds reads an option written as a whole number of seconds.
//
// It cannot fail, and that is the point. Every caller stores its result
// unconditionally on every reconfiguration -- SetPushServiceConfig runs again
// whenever the push service manager reconfigures -- so an option that is
// absent, unparseable or out of range has to produce the default rather than an
// error to ignore. Writing the setting only when it parsed would leave whatever
// an earlier config installed still in force after the line was deleted or
// corrupted, and deleting a line has to undo it.
//
// A value outside [minimum, maximum] falls back to the default instead of being
// clamped to the nearest bound. Clamping would leave a server running on a
// number nobody wrote and nothing announced: an operator who asked for an hour
// and got the documented default at least has behaviour the documentation
// explains, where one who got a silently clamped five minutes has neither.
//
// The bounds are applied to the number of seconds rather than to the duration
// it converts to, so that a wildly large value cannot overflow into something
// small enough to look acceptable.
func (config *PushServiceConfig) GetSeconds(option string, fallback, minimum, maximum time.Duration) time.Duration {
	seconds, err := config.GetInt(option)
	if err != nil {
		return fallback
	}
	if seconds < int(minimum/time.Second) || seconds > int(maximum/time.Second) {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

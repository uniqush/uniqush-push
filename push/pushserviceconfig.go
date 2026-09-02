package push

import "errors"
import "github.com/uniqush/goconf/conf"

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

package push

import "errors"
import "github.com/uniqush/goconf/conf"

// PushServiceConfig accesses the section 'name' of the given ConfigFile
type PushServiceConfig struct {
	c    *conf.ConfigFile
	name string
}

// NewPushServiceConfig returns an accessor for the given section 'name' of the unserialized config file
func NewPushServiceConfig(c *conf.ConfigFile, name string) *PushServiceConfig {
	return &PushServiceConfig{
		c:    c,
		name: name,
	}
}

// GetString will return a string for the given option from this push service's section of the configuration file.
func (config *PushServiceConfig) GetString(option string) (string, error) {
	if config.c == nil {
		return "", errors.New("No config")
	}
	return config.c.GetString(config.name, option)
}

// GetInt will return an integer for the given option from this push service's section of the configuration file.
func (config *PushServiceConfig) GetInt(option string) (int, error) {
	if config.c == nil {
		return 0, errors.New("No config")
	}
	return config.c.GetInt(config.name, option)
}

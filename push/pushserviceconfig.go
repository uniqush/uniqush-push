package push

import "errors"
import "github.com/uniqush/goconf/conf"

type PushServiceConfig struct {
	c    *conf.ConfigFile
	name string
}

func NewPushServiceConfig(c *conf.ConfigFile, name string) *PushServiceConfig {
	return &PushServiceConfig{
		c:    c,
		name: name,
	}
}

func (config *PushServiceConfig) GetString(option string) (string, error) {
	if config.c == nil {
		return "", errors.New("No config")
	}
	return config.c.GetString(config.name, option)
}

func (config *PushServiceConfig) GetInt(option string) (int, error) {
	if config.c == nil {
		return 0, errors.New("No config")
	}
	return config.c.GetInt(config.name, option)
}

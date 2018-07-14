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

func (self *PushServiceConfig) GetString(option string) (string, error) {
	if self.c == nil {
		return "", errors.New("No config")
	}
	return self.c.GetString(self.name, option)
}

func (self *PushServiceConfig) GetInt(option string) (int, error) {
	if self.c == nil {
		return 0, errors.New("No config")
	}
	return self.c.GetInt(self.name, option)
}

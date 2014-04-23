package db

import (
	"fmt"
	"net"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/uniqush/uniqush-push/push"
)

func init() {
	RegisterDatabase("redis", newRedisPushDatabase)
}

type redisPushDatabase struct {
	pool *redis.Pool
}

func newRedisPushDatabase(dbconfig *DatabaseConfig) (db PushDatabase, err error) {
	host := "localhost"
	port := 6379
	password := ""
	dbname := "0"

	if dbconfig != nil {
		if len(dbconfig.Host) > 0 {
			host = dbconfig.Host
		}
		if len(dbconfig.Password) > 0 {
			password = dbconfig.Password
		}
		if len(dbconfig.Database) > 0 {
			dbname = dbconfig.Database
		}
		if dbconfig.Port > 0 {
			port = dbconfig.Port
		}
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%v", port))
	dial := func() (redis.Conn, error) {
		c, err := redis.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		if len(password) > 0 {
			if _, err := c.Do("AUTH", password); err != nil {
				c.Close()
				return nil, err
			}
		}
		if _, err := c.Do("SELECT", dbname); err != nil {
			c.Close()
			return nil, err
		}
		return c, err
	}
	testOnBorrow := func(c redis.Conn, t time.Time) error {
		_, err := c.Do("PING")
		return err
	}

	pool := &redis.Pool{
		MaxIdle:      3,
		IdleTimeout:  240 * time.Second,
		Dial:         dial,
		TestOnBorrow: testOnBorrow,
	}

	c, err := dial()
	if err != nil {
		err = fmt.Errorf("unable to connect to redis: %v", err)
		return
	}
	defer c.Close()
	_, err = c.Do("PING")
	if err != nil {
		err = fmt.Errorf("unable to connect to redis: %v", err)
		return
	}

	ret := new(redisPushDatabase)
	ret.pool = pool
	db = ret
	return
}

func buildProviderKey(provider push.Provider) string {
	return fmt.Sprintf("provider:%v:%v:%v", provider.PushService(), provider.Service(), provider.UniqId())
}

func buildDeliveryPointKey(dp push.DeliveryPoint) string {
	return fmt.Sprintf("dp:%v:%v:%v:%v:%v", dp.PushService(), dp.Service(), dp.Provider(), dp.Subscriber(), dp.UniqId())
}

func buildDeliveryPointLookUpKey(provider push.Provider, uniqId string) string {
	return fmt.Sprintf("dp:%v:%v:%v:*:%v", provider.PushService(),
		provider.Service(), provider.UniqId(), uniqId)
}

func (self *redisPushDatabase) AddProvider(provider push.Provider) error {
	conn := self.pool.Get()
	defer conn.Close()

	key := buildProviderKey(provider)
	ps, err := push.GetPushService(provider)
	if err != nil {
		return err
	}
	data, err := ps.MarshalProvider(provider)
	if err != nil {
		return err
	}
	reply, err := conn.Do("SET", key, data)
	if err != nil {
		return err
	}
	n, err := redis.String(reply, err)
	if err != nil {
		return err
	}
	if n != "OK" {
		err = fmt.Errorf("Trying to add provider %v. received message: %v", provider.UniqId(), n)
		return err
	}
	return nil
}

func (self *redisPushDatabase) DelProvider(provider push.Provider) error {
	conn := self.pool.Get()
	defer conn.Close()

	key := buildProviderKey(provider)
	ps, err := push.GetPushService(provider)
	if err != nil {
		return err
	}
	data, err := ps.MarshalProvider(provider)
	if err != nil {
		return err
	}
	reply, err := conn.Do("DEL", key, data)
	if err != nil {
		return err
	}
	n, err := redis.Int(reply, err)
	if err != nil {
		return err
	}
	if n != 1 {
		err = fmt.Errorf("Trying to del provider %v. But set %v values in redis", provider.UniqId(), n)
		return err
	}
	return nil
}

package db

import (
	"fmt"
	"sync"
)

type databaseManager struct {
	dbfactories map[string]func(dbconfig *DatabaseConfig) (db PushDatabase, err error)
	lock        sync.RWMutex
}

var dbmngr databaseManager

func RegisterDatabase(engine string, f func(dbconfig *DatabaseConfig) (db PushDatabase, err error)) error {
	return dbmngr.RegisterDatabase(engine, f)
}

func (self *databaseManager) RegisterDatabase(engine string, f func(dbconfig *DatabaseConfig) (db PushDatabase, err error)) error {
	self.lock.Lock()
	defer self.lock.Unlock()

	if self.dbfactories == nil {
		self.dbfactories = make(map[string]func(dbconfig *DatabaseConfig) (db PushDatabase, err error), 5)
	}
	self.dbfactories[engine] = f
	return nil
}

func GetPushDatabase(config *DatabaseConfig) (db PushDatabase, err error) {
	return dbmngr.GetPushDatabase(config)
}

func (self *databaseManager) GetPushDatabase(config *DatabaseConfig) (db PushDatabase, err error) {
	if self == nil || len(self.dbfactories) == 0 {
		err = fmt.Errorf("no database is registered")
		return
	}
	if f, ok := self.dbfactories[config.Engine]; ok {
		return f(config)
	}
	err = fmt.Errorf("unknown database: ", config.Engine)
	return
}

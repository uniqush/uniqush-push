package db

import "github.com/uniqush/uniqush-push/push"

type LayeredPushDatabase struct {
	dbs []PushDatabase
}

// The last argument will be used as the final database.
// Others will be used as caches
func NewLayeredPushDatabase(dbs ...PushDatabase) PushDatabase {
	ret := new(LayeredPushDatabase)
	ret.dbs = dbs
	return ret
}

func (self *LayeredPushDatabase) mapAllSuccess(f func(PushDatabase) error) error {
	for _, db := range self.dbs {
		err := f(db)
		if err != nil {
			return err
		}
	}
	return nil
}

func (self *LayeredPushDatabase) reverseMapAllSuccess(f func(PushDatabase) error) error {
	for i := len(self.dbs) - 1; i >= 0; i-- {
		db := self.dbs[i]
		err := f(db)
		if err != nil {
			return err
		}
	}
	return nil
}

func (self *LayeredPushDatabase) mapOneSuccess(f func(PushDatabase) error) error {
	var err error
	for _, db := range self.dbs {
		err = f(db)
		if err == nil {
			return nil
		}
	}
	return err
}

func (self *LayeredPushDatabase) AddProvider(provider push.Provider) error {
	return self.mapAllSuccess(func(db PushDatabase) error {
		return db.AddProvider(provider)
	})
}

func (self *LayeredPushDatabase) UpdateProvider(provider push.Provider) error {
	return self.mapAllSuccess(func(db PushDatabase) error {
		return db.UpdateProvider(provider)
	})
}

func (self *LayeredPushDatabase) DelProvider(provider push.Provider) error {
	return self.mapAllSuccess(func(db PushDatabase) error {
		return db.DelProvider(provider)
	})
}

func (self *LayeredPushDatabase) UpdateDeliveryPoint(dp push.DeliveryPoint) error {
	return self.mapAllSuccess(func(db PushDatabase) error {
		return db.UpdateDeliveryPoint(dp)
	})
}

func (self *LayeredPushDatabase) AddPairs(pairs ...*ProviderDeliveryPointPair) (newPairs []*ProviderDeliveryPointPair, err error) {
	newPairs = pairs
	err = self.reverseMapAllSuccess(func(db PushDatabase) error {
		var e error
		newPairs, e = db.AddPairs(newPairs...)
		return e
	})
	if err != nil {
		newPairs = nil
	}
	return
}

func (self *LayeredPushDatabase) LoopUpPairs(service, subscriber string) (pairs []*ProviderDeliveryPointPair, err error) {
	err = self.mapOneSuccess(func(db PushDatabase) error {
		var e error
		pairs, e = db.LoopUpPairs(service, subscriber)
		return e
	})
	if err != nil {
		pairs = nil
	}
	return
}

func (self *LayeredPushDatabase) DelDeliveryPoint(p push.Provider, dp push.DeliveryPoint) error {
	err := self.mapAllSuccess(func(db PushDatabase) error {
		return db.DelDeliveryPoint(p, dp)
	})
	return err
}

func (self *LayeredPushDatabase) LookupDeliveryPointWithUniqId(provider push.Provider, uniqId string) (dps []push.DeliveryPoint, err error) {
	err = self.mapOneSuccess(func(db PushDatabase) error {
		var e error
		dps, e = db.LookupDeliveryPointWithUniqId(provider, uniqId)
		return e
	})
	if err != nil {
		dps = nil
	}
	return
}

func (self *LayeredPushDatabase) Close() error {
	for _, db := range self.dbs {
		db.Close()
	}
	return nil
}

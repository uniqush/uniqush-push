/*
 * Copyright 2012 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package db

import (
	"github.com/uniqush/cache"
	. "github.com/uniqush/uniqush-push/push"
	"time"
)

type pushRawDatabaseCache struct {
	pspCache  *cache.Cache
	dpCache   *cache.Cache
	srvSub2Dp *cache.Cache
	srv2Psp   *cache.Cache
	dbwriter  pushRawDatabaseWriter
	dbreader  pushRawDatabaseReader
}

type pspFlusher struct {
	cdb *pushRawDatabaseCache
}

func (self *pspFlusher) Add(key string, value interface{}) {
	if psp, ok := value.(*PushServiceProvider); ok {
		self.cdb.dbwriter.SetPushServiceProvider(psp)
	}
}

func (self *pspFlusher) Remove(key string) {
	self.cdb.dbwriter.RemovePushServiceProvider(key)
}

type dpFlusher struct {
	cdb *pushRawDatabaseCache
}

func (self *dpFlusher) Add(key string, value interface{}) {
	if psp, ok := value.(*DeliveryPoint); ok {
		self.cdb.dbwriter.SetDeliveryPoint(psp)
	}
}

func (self *dpFlusher) Remove(key string) {
	self.cdb.dbwriter.RemoveDeliveryPoint(key)
}

func NewpushRawDatabaseCache(c *DatabaseConfig,
	dbreader pushRawDatabaseReader,
	dbwriter pushRawDatabaseWriter) (*pushRawDatabaseCache, error) {
	cacheSize := 1024
	flushPeriod := 600
	leastDirty := 128
	if c != nil {
		cacheSize = c.CacheSize
		flushPeriod = int(c.EverySec)
		leastDirty = c.LeastDirty
	}

	cdb := new(pushRawDatabaseCache)
	cdb.dbreader = dbreader
	cdb.dbwriter = dbwriter
	pspflusher := &pspFlusher{cdb: cdb}
	dpflusher := &dpFlusher{cdb: cdb}
	cdb.pspCache = cache.New(cacheSize, leastDirty, time.Duration(flushPeriod)*time.Second, pspflusher)
	cdb.dpCache = cache.New(cacheSize, leastDirty, time.Duration(flushPeriod)*time.Second, dpflusher)

	// We will flush them manually
	cdb.srvSub2Dp = cache.New(cacheSize, -1, time.Duration(0)*time.Second, nil)
	cdb.srv2Psp = cache.New(cacheSize, -1, time.Duration(0)*time.Second, nil)
	return cdb, nil
}

func (cdb *pushRawDatabaseCache) GetDeliveryPoint(dp string) (ret *DeliveryPoint, err error) {
	if dpi := cdb.dpCache.Get(dp); dpi == nil {
		ret, err = cdb.dbreader.GetDeliveryPoint(dp)
		if err != nil {
			return nil, err
		}
		cdb.dpCache.Set(dp, ret.Name())
	} else {
		ret = dpi.(*DeliveryPoint)
	}
	return ret, nil
}

func (cdb *pushRawDatabaseCache) GetPushServiceProvider(psp string) (ret *PushServiceProvider, err error) {
	if pspi := cdb.pspCache.Get(psp); pspi == nil {
		ret, err = cdb.dbreader.GetPushServiceProvider(psp)
		if err != nil {
			return nil, err
		}
		cdb.dpCache.Set(psp, ret.Name())
	} else {
		ret = pspi.(*PushServiceProvider)
	}
	return ret, nil
}

func (cdb *pushRawDatabaseCache) SetDeliveryPoint(dp *DeliveryPoint) error {
	cdb.dpCache.Set(dp.Name(), dp)
	return nil
}

func (cdb *pushRawDatabaseCache) SetPushServiceProvider(psp *PushServiceProvider) error {
	cdb.pspCache.Set(psp.Name(), psp)
	return nil
}

func (cdb *pushRawDatabaseCache) RemoveDeliveryPoint(dp string) error {
	cdb.dpCache.Delete(dp)
	return nil
}

func (cdb *pushRawDatabaseCache) RemovePushServiceProvider(psp string) error {
	cdb.pspCache.Delete(psp)
	return nil
}

func (cdb *pushRawDatabaseCache) GetDeliveryPointsNameByServiceSubscriber(srv, sub string) (dps map[string][]string, err error) {
	key := srv + ":" + sub
	if i := cdb.srvSub2Dp.Get(key); i == nil {
		dps, err = cdb.dbreader.GetDeliveryPointsNameByServiceSubscriber(srv, sub)
		if err != nil {
			return nil, err
		}
		cdb.srvSub2Dp.Set(key, dps)
	} else {
		dps = i.(map[string][]string)
	}
	return dps, nil
}

/*
func (cdb *pushRawDatabaseCache) AddDeliveryPointToServiceSubscriber(srv, sub, dp string) error {
}
*/

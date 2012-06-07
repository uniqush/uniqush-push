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

package pushdb

import (
	. "github.com/monnand/uniqush/pushsys"
	"github.com/monnand/uniqush/cache"
	"fmt"
)

type PushDatabaseCache struct {
	pspCache *cache.Cache
	dpCache *cache.Cache
	srvSub2Dp *cache.Cache
	srv2Psp *cache.Cache
	dbwriter PushDatabaseWriter
	dbreader PushDatabaseReader
}

type pspFluser struct {
	cdb *PushDatabaseCache
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
	cdb *PushDatabaseCache
}

func (self *dpFlusher) Add(key string, value interface{}) {
	if psp, ok := value.(*DeliveryPoint); ok {
		self.cdb.dbwriter.SetDeliveryPoint(psp)
	}
}

func (self *pspFlusher) Remove(key string) {
	self.cdb.dbwriter.RemoveDeliveryPoint(key)
}

func NewPushDatabaseCache(c *DatabaseConfig,
						  dbreader PushDatabaseReader,
						  dbwriter PushDatabaseWriter) (*PushDatabaseCache, error) {
	cacheSize := 1024
	flushPeriod := 600
	leastDirty := 128
	if c != nil {
		cacheSize = c.CacheSize
		flushPeriod = int(c.EverySec)
		leastDirty = c.LeastDirty
	}

	cdb := new(PushDatabaseCache)
	cdb.dbreader = dbreader
	cdb.dbwriter = dbwriter
	pspflusher := &pspFlusher{cdb:cdb}
	dpflusher := &dpFlusher{cdb:cdb}
	cdb.pspCache = cache.New(cacheSize, leastDirty, flushPeriod * time.Second, pspflusher)
	cdb.dpCache = cache.New(cacheSize, leastDirty, flushPeriod * time.Second, dpflusher)
	cdb.srvSub2Dp = cache.New(cacheSize, leastDirty, flushPeriod * time.Second, nil)
	cdb.srv2Psp = cache.New(cacheSize, leastDirty, flushPeriod * time.Second, nil)
	return cdb
}

func (cdb *PushDatabaseCache) GetDeliveryPoint(dp string) (ret *DeliveryPoint, err error) {
	if dpi := cdb.dpCache.Get(dp); dpi == nil {
		ret, err = dbreader.GetDeliveryPoint(dp)
		if err != nil {
			return nil, err
		}
		cdb.dpCache.Set(dp, ret.Name())
	} else {
		ret = dpi.(*DeliveryPoint)
	}
	return ret, nil
}

func (cdb *PushDatabaseCache) GetPushServiceProvider(psp string) (ret *PushServiceProvider, err error) {
	if pspi := cdb.pspCache.Get(psp); pspi == nil {
		ret, err = dbreader.GetPushServiceProvider(psp)
		if err != nil {
			return nil, err
		}
		cdb.dpCache.Set(psp, ret.Name())
	} else {
		ret = dpi.(*PushServiceProvider)
	}
	return ret, nil
}

func (cdb *PushDatabaseCache) SetDeliveryPoint(dp *DeliveryPoint) error {
	cdb.dpCache.Set(dp.Name(), dp)
	return nil
}

func (cdb *PushDatabaseCache) SetPushServiceProvider(psp *PushServiceProvider) error {
	cdb.pspCache.Set(psp.Name(), psp)
	return nil
}

func (cdb *PushServiceProvider) RemoveDeliveryPoint(dp string) error {
	cdb.dpCache.Delete(dp)
	return nil
}

func (cdb *PushServiceProvider) RemovePushServiceProvider(psp string) error {
	cdb.pspCache.Delete(psp)
	return nil
}


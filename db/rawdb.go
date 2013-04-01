/*
 * Copyright 2011 Nan Deng
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
	. "github.com/uniqush/uniqush-push/push"
)

// In general, an push database stores the relationships between
// Service, Subscriber, Push Service Provider and Delivery Point
//
// In an uniqush database, there are one or more Services.
//
// Each Service has a set of Subscriber.
//
// Each Service has a set of Push Service Provider.
//
// Each Service-Subscriber pair, has a set of Delivery Points. When
// uniqush want to push some message to some Subscriber under certain
// Service, it will deliver the message to all Delivery Points under
// the associated Service-Subscriber pair
//
// Each Service-Delivery-Points pair, has one Push Service Provider.
// When we need to deliver some message to a certain delivery point,
// we will use its associated Push Service Provider to send.
//
// For performance consideration, the database may become inconsistent
// if the user did a wrong operation. For example, add a non-exist
// delivery point to Service-Subscriber pair.
//

// Danger: writing wrong data may leads to inconsistent
type pushRawDatabaseWriter interface {
	SetDeliveryPoint(dp *DeliveryPoint) error
	SetPushServiceProvider(psp *PushServiceProvider) error
	RemoveDeliveryPoint(dp string) error
	RemovePushServiceProvider(psp string) error

	AddDeliveryPointToServiceSubscriber(srv, sub, dp string) error
	RemoveDeliveryPointFromServiceSubscriber(srv, sub, dp string) error
	SetPushServiceProviderOfServiceDeliveryPoint(srv, dp, psp string) error
	RemovePushServiceProviderOfServiceDeliveryPoint(srv, dp string) error

	AddPushServiceProviderToService(srv, psp string) error
	RemovePushServiceProviderFromService(srv, psp string) error

	FlushCache() error
}

// These methods should be fast!
type pushRawDatabaseReader interface {
	GetDeliveryPoint(name string) (*DeliveryPoint, error)
	GetPushServiceProvider(name string) (*PushServiceProvider, error)

	GetDeliveryPointsNameByServiceSubscriber(srv, sub string) (map[string][]string, error)
	GetPushServiceProviderNameByServiceDeliveryPoint(srv, dp string) (string, error)

	GetPushServiceProvidersByService(srv string) ([]string, error)
}

type pushRawDatabase interface {
	pushRawDatabaseReader
	pushRawDatabaseWriter
}

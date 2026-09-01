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
	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/push"
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

// ServiceProvider is one member of a service's provider set, paired with the
// record behind it. Provider is nil when the name has no record -- an
// interrupted write, which /checkdb reports as a dangling provider.
type ServiceProvider struct {
	Name     string
	Provider *push.PushServiceProvider
}

// Danger: writing wrong data may leads to inconsistent
type pushRawDatabaseWriter interface { //nolint:staticcheck
	// SetDeliveryPoint serializes the delivery point, and saves the serialized delivery point based on its name.
	SetDeliveryPoint(dp *push.DeliveryPoint) error
	SetPushServiceProvider(psp *push.PushServiceProvider) error
	RemoveDeliveryPoint(dp string) error
	RemovePushServiceProvider(psp string) error
	RebuildServiceSet() error

	AddDeliveryPointToServiceSubscriber(srv, sub, dp string) error
	RemoveDeliveryPointFromServiceSubscriber(srv, sub, dp string) error
	// RemoveMissingDeliveryPointFromServiceSubscriber cleans up after a delivery
	// point whose record has already gone: it drops the dangling name from the
	// subscriber's set and deletes the counter that was tracking it.
	//
	// This is the complete teardown for that case. Deleting only
	// delivery.point:<dp>, which is what the read path used to do, leaves the
	// name in srv.sub-2-dp and leaks delivery.point.counter -- a garbage
	// collector that creates garbage.
	//
	// The logger may be nil; implementations normalise it. It is only written to
	// when redis fails, so requiring it would put a panic on the error path.
	RemoveMissingDeliveryPointFromServiceSubscriber(srv, sub, dp string, logger log.Logger)
	SetPushServiceProviderOfServiceDeliveryPoint(srv, dp, psp string) error
	RemovePushServiceProviderOfServiceDeliveryPoint(srv, dp string) error

	AddPushServiceProviderToService(srv, psp string) error
	// SetPushServiceProviderOfService installs psp as a provider of srv, and
	// removes the providers that decide names, as one atomic operation.
	//
	// decide is handed the service's current providers, read inside the
	// transaction, and returns the names to supersede -- or an error, which
	// abandons the whole thing and is returned to the caller. Splitting it this
	// way keeps the policy (which providers conflict, and whether replacing one
	// was asked for) out of the layer that knows about redis keys.
	//
	// The atomicity is not decoration. Installing a replacement is a
	// read-modify-write of one service's provider set, and dblock is per
	// process, so two uniqush instances against one redis have nothing else
	// stopping them from interleaving.
	SetPushServiceProviderOfService(srv string, psp *push.PushServiceProvider,
		decide func([]ServiceProvider) ([]string, error)) error
	RemovePushServiceProviderFromService(srv, psp string) error

	FlushCache() error
}

// These methods should be fast!
type pushRawDatabaseReader interface { //nolint:staticcheck
	GetDeliveryPoint(name string) (*push.DeliveryPoint, error)
	GetPushServiceProvider(name string) (*push.PushServiceProvider, error)
	GetServiceNames() ([]string, error)
	GetPushServiceProviderConfigs([]string) ([]*push.PushServiceProvider, []error)
	GetSubscriptions(queryServices []string, subscriber string, logger log.Logger) ([]map[string]string, error)

	GetDeliveryPointsNameByServiceSubscriber(srv, sub string) (map[string][]string, error)
	GetPushServiceProviderNameByServiceDeliveryPoint(srv, dp string) (string, error)

	GetPushServiceProvidersByService(srv string) ([]string, error)

	// CheckConsistency scans the whole database and reports what does not add
	// up. Read-only, and implemented alongside the key layout rather than in
	// pushdb.go because every check is a statement about how the keys relate.
	CheckConsistency() (*ConsistencyReport, error)
}

type pushRawDatabase interface {
	pushRawDatabaseReader
	pushRawDatabaseWriter
}

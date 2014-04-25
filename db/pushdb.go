package db

import "github.com/uniqush/uniqush-push/push"

const (
	CACHE_TYPE_ALWAYS_IN = iota
	CACHE_TYPE_LRU
	CACHE_TYPE_RANDOM
)

type DatabaseConfig struct {
	Engine    string `toml:"engine"`
	Host      string `toml:"host"`
	Port      int    `toml:"port"`
	Username  string `toml:"username"`
	Password  string `tomel:"password"`
	Database  string `toml:"database"`
	IsCache   bool   `toml:"-"`
	CacheType int    `toml:"-"`
}

type PushDatabase interface {
	AddProvider(provider push.Provider) error
	// Update the provider with same uniqid
	UpdateProvider(provider push.Provider) error
	UpdateDeliveryPoint(dp push.DeliveryPoint) error
	DelProvider(provider push.Provider) error
	AddPairs(pairs ...*ProviderDeliveryPointPair) (newpairs []*ProviderDeliveryPointPair, err error)
	LoopUpPairs(service, subscriber string) (pairs []*ProviderDeliveryPointPair, err error)
	// provider could be nil, in which case the database should pair
	// dp with a provider following the same rule as in AddPairs()
	DelDeliveryPoint(provider push.Provider, dp push.DeliveryPoint) error
	/*
		UpdateProvider(provider push.Provider) error
		UpdateDeliveryPoint(dp push.DeliveryPoint) error
		LoopUpDeliveryPoint(provider push.Provider, uniqId string) (dp push.DeliveryPoint, err error)

		LoopUpPairs(service, subscriber string) (pairs []*ProviderDeliveryPointPair, err error)

		Flush() error
	*/
}

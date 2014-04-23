package db

import "github.com/uniqush/uniqush-push/push"

type ProviderDeliveryPointPair struct {
	Provider      push.Provider
	DeliveryPoint push.DeliveryPoint
}

type DatabaseConfig struct {
	Engine   string `toml:"engine"`
	Host     string `toml:"host"`
	Port     int    `toml:"port"`
	Username string `toml:"username"`
	Password string `tomel:"password"`
	Database string `toml:"database"`
}

type PushDatabase interface {
	AddProvider(provider push.Provider) error
	DelProvider(provider push.Provider) error
	/*
		UpdataProvider(provider push.Provider) error

		AddDeliveryPoint(dp push.DeliveryPoint) error
		DelDeliveryPoint(dp push.DeliveryPoint) error
		UpdateDeliveryPoint(dp push.DeliveryPoint) error
		LoopUpDeliveryPoint(provider push.Provider, uniqId string) (dp push.DeliveryPoint, err error)

		LoopUpPairs(service, subscriber string) (pairs []*ProviderDeliveryPointPair, err error)

		Flush() error
	*/
}

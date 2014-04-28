package push

import (
	"fmt"
	"time"
)

type ErrorRetry struct {
	RetryAfter time.Duration
}

func (self *ErrorRetry) Error() string {
	return fmt.Sprintf("retry after %v", self.RetryAfter)
}

type UpdateDeliveryPoint struct {
}

func (self *UpdateDeliveryPoint) Error() string {
	return "update delivery point"
}

type UpdateProvider struct {
}

func (self *UpdateProvider) Error() string {
	return "update provider"
}

package common

// Status codes for the binary API
const (
	Status0Success            = 0
	Status1ProcessingError    = 1
	Status2MissingDeviceToken = 2
	Status3MissingTopic       = 3
	Status4MissingPayload     = 4
	Status5InvalidTokenSize   = 5
	Status6InvalidTopicSize   = 6
	Status7InvalidPayloadSize = 7
	Status8Unsubscribe        = 8
)

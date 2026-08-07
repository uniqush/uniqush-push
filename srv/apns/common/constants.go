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

// Values for the apns-push-type header.
//
// Apple has required this header on watchOS since watchOS 6 and recommends it
// everywhere else, with the blunt instruction to "send an apns-push-type header
// with each push". Omitting it on a background push to iOS 13+ makes APNs return
// 200 and then drop the notification, which is invisible in logs.
//
// See https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns
const (
	PushTypeAlert        = "alert"
	PushTypeBackground   = "background"
	PushTypeComplication = "complication"
	PushTypeControls     = "controls"
	PushTypeFileProvider = "fileprovider"
	PushTypeLiveActivity = "liveactivity"
	PushTypeLocation     = "location"
	PushTypeMDM          = "mdm"
	PushTypePushToTalk   = "pushtotalk"
	PushTypeVoIP         = "voip"
	PushTypeWidgets      = "widgets"
)

// DefaultPushType is used when a push does not specify one. "alert" is the
// safe default: it is what the overwhelming majority of uniqush pushes are, and
// it is what APNs assumed before the header existed.
const DefaultPushType = PushTypeAlert

// APNs priorities. 10 delivers immediately; 5 is power-aware.
const (
	PriorityImmediate  = "10"
	PriorityPowerAware = "5"
)

// validPushTypes is the set accepted by APNs. Sending an unrecognised value
// earns a 400 InvalidPushType, so reject it locally where the error is useful.
var validPushTypes = map[string]bool{
	PushTypeAlert:        true,
	PushTypeBackground:   true,
	PushTypeComplication: true,
	PushTypeControls:     true,
	PushTypeFileProvider: true,
	PushTypeLiveActivity: true,
	PushTypeLocation:     true,
	PushTypeMDM:          true,
	PushTypePushToTalk:   true,
	PushTypeVoIP:         true,
	PushTypeWidgets:      true,
}

// IsValidPushType reports whether pushType is a push type APNs recognises.
func IsValidPushType(pushType string) bool {
	return validPushTypes[pushType]
}

// ValidPushTypes returns the accepted push types, for error messages.
func ValidPushTypes() []string {
	types := make([]string, 0, len(validPushTypes))
	for t := range validPushTypes {
		types = append(types, t)
	}
	return types
}

// PriorityForPushType returns the apns-priority value to use for a push type.
//
// This is not a free choice. Apple's documentation for background pushes says
// "Always use priority 5. Using priority 10 is an error", which APNs enforces
// with a 400 BadPriority. Every other push type defaults to immediate delivery.
func PriorityForPushType(pushType string) string {
	if pushType == PushTypeBackground {
		return PriorityPowerAware
	}
	return PriorityImmediate
}

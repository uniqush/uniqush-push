package main

// These are constants with codes for a uniqush response type.
// nolint: golint
const (
	/* Not errors */

	UNIQUSH_SUCCESS            = "UNIQUSH_SUCCESS"
	UNIQUSH_REMOVE_INVALID_REG = "UNIQUSH_REMOVE_INVALID_REG"
	UNIQUSH_UPDATE_UNSUBSCRIBE = "UNIQUSH_UPDATE_UNSUBSCRIBE"

	/* Errors */

	UNIQUSH_ERROR_GENERIC            = "UNIQUSH_ERROR_GENERIC"
	UNIQUSH_ERROR_EMPTY_NOTIFICATION = "UNIQUSH_ERROR_EMPTY_NOTIFICATION"
	UNIQUSH_ERROR_DATABASE           = "UNIQUSH_ERROR_DATABASE"
	UNIQUSH_ERROR_FAILED_RETRY       = "UNIQUSH_ERROR_FAILED_RETRY"

	UNIQUSH_ERROR_BUILD_PUSH_SERVICE_PROVIDER  = "UNIQUSH_ERROR_BUILD_PUSH_SERVICE_PROVIDER"
	UNIQUSH_ERROR_UPDATE_PUSH_SERVICE_PROVIDER = "UNIQUSH_ERROR_UPDATE_PUSH_SERVICE_PROVIDER"

	UNIQUSH_ERROR_BAD_DELIVERY_POINT    = "UNIQUSH_ERROR_BAD_DELIVERY_POINT"
	UNIQUSH_ERROR_BUILD_DELIVERY_POINT  = "UNIQUSH_ERROR_BUILD_DELIVERY_POINT"
	UNIQUSH_ERROR_UPDATE_DELIVERY_POINT = "UNIQUSH_ERROR_UPDATE_DELIVERY_POINT"

	UNIQUSH_ERROR_CANNOT_GET_SERVICE           = "UNIQUSH_ERROR_CANNOT_GET_SERVICE"
	UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER        = "UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER"
	UNIQUSH_ERROR_CANNOT_GET_DELIVERY_POINT_ID = "UNIQUSH_ERROR_CANNOT_GET_DELIVERY_POINT_ID"

	UNIQUSH_ERROR_NO_DEVICE                = "UNIQUSH_ERROR_NO_DEVICE"
	UNIQUSH_ERROR_NO_DELIVERY_POINT        = "UNIQUSH_ERROR_NO_DELIVERY_POINT"
	UNIQUSH_ERROR_NO_PUSH_SERVICE_PROVIDER = "UNIQUSH_ERROR_NO_PUSH_SERVICE_PROVIDER"
	UNIQUSH_ERROR_NO_SUBSCRIBER            = "UNIQUSH_ERROR_NO_SUBSCRIBER"
	UNIQUSH_ERROR_NO_PUSH_SERVICE_TYPE     = "UNIQUSH_ERROR_NO_PUSH_SERVICE_TYPE"
)

// APIResponseDetails is used to represent responses of various APIs. Different APIs use different subsets of fields.
type APIResponseDetails struct {
	RequestID           *string `json:"requestId,omitempty"`
	Service             *string `json:"service,omitempty"`
	From                *string `json:"from,omitempty"`
	Subscriber          *string `json:"subscriber,omitempty"`
	PushServiceProvider *string `json:"pushServiceProvider,omitempty"`
	DeliveryPoint       *string `json:"deliveryPoint,omitempty"`
	MessageID           *string `json:"messageId,omitempty"`
	Code                string  `json:"code"`
	ErrorMsg            *string `json:"errorMsg,omitempty"`
	ModifiedDp          bool    `json:"modifiedDp,omitempty"`
}

// PreviewAPIResponseDetails represents the response of /preview. It contains a representation of the payload that would be sent to external push services
type PreviewAPIResponseDetails struct {
	Code     string      `json:"code"`
	Payload  interface{} `json:"payload,omitempty"`
	ErrorMsg *string     `json:"errorMsg,omitempty"`
}

func strPtrOfErr(e error) *string {
	if e == nil {
		return nil
	}
	s := e.Error()
	return &s
}

// APIResponseHandler is interface for collecting API responses
type APIResponseHandler interface {
	AddDetailsToHandler(v APIResponseDetails)
	ToJSON() []byte
}

// NullAPIResponseHandler is an APIResponseHandler implementation that does nothing.
type NullAPIResponseHandler struct{}

var _ APIResponseHandler = &NullAPIResponseHandler{}

// AddDetailsToHandler does nothing for NullAPIResponseHandler
func (handler *NullAPIResponseHandler) AddDetailsToHandler(v APIResponseDetails) {}

// ToJSON returns an empty list for NullAPIResponseHandler
func (handler *NullAPIResponseHandler) ToJSON() []byte {
	return []byte{}
}

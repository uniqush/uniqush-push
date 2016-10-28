package main

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

	UNIQUSH_ERROR_CANNOT_GET_SERVICE    = "UNIQUSH_ERROR_CANNOT_GET_SERVICE"
	UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER = "UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER"

	UNIQUSH_ERROR_NO_DEVICE                = "UNIQUSH_ERROR_NO_DEVICE"
	UNIQUSH_ERROR_NO_DELIVERY_POINT        = "UNIQUSH_ERROR_NO_DELIVERY_POINT"
	UNIQUSH_ERROR_NO_PUSH_SERVICE_PROVIDER = "UNIQUSH_ERROR_NO_PUSH_SERVICE_PROVIDER"
	UNIQUSH_ERROR_NO_SERVICE               = "UNIQUSH_ERROR_NO_SERVICE"
	UNIQUSH_ERROR_NO_SUBSCRIBER            = "UNIQUSH_ERROR_NO_SUBSCRIBER"
	UNIQUSH_ERROR_NO_PUSH_SERVICE_TYPE     = "UNIQUSH_ERROR_NO_PUSH_SERVICE_TYPE"
)

type ApiResponseDetails struct {
	RequestId           *string `json:"requestId,omitempty"`
	Service             *string `json:"service,omitempty"`
	From                *string `json:"from,omitempty"`
	Subscriber          *string `json:"subscriber,omitempty"`
	PushServiceProvider *string `json:"pushServiceProvider,omitempty"`
	DeliveryPoint       *string `json:"deliveryPoint,omitempty"`
	MessageId           *string `json:"messageId,omitempty"`
	Code                string  `json:"code"`
	ErrorMsg            *string `json:"errorMsg,omitempty"`
	ModifiedDp          bool    `json:"modifiedDp,omitempty"`
}

type PreviewApiResponseDetails struct {
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

type ApiResponseHandler interface {
	AddDetailsToHandler(v ApiResponseDetails)
	ToJSON() []byte
}

type NullApiResponseHandler struct{}

var _ ApiResponseHandler = (*NullApiResponseHandler)(nil)

func (self *NullApiResponseHandler) AddDetailsToHandler(v ApiResponseDetails) {}

func (self *NullApiResponseHandler) ToJSON() []byte {
	return []byte{}
}

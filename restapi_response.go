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
)

type ApiResponseDetails struct {
	RequestId           *string `json:"requestId"`
	Service             *string `json:"service"`
	From                *string `json:"from"`
	Subscriber          *string `json:"subscriber"`
	PushServiceProvider *string `json:"pushServiceProvider"`
	DeliveryPoint       *string `json:"deliveryPoint"`
	MessageId           *string `json:"messageId"`
	Code                string  `json:"code"`
	ErrorMsg            *string `json:"errorMsg"`
}

func strPtrOfErr(e error) *string {
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

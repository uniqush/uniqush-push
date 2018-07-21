package main

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/uniqush/log"
)

// APIPushResponseHandler records information about push notification attempts to generate a JSON response.
type APIPushResponseHandler struct {
	response APIPushResponse
	logger   log.Logger
	mutex    sync.Mutex
}

var _ APIResponseHandler = &APIPushResponseHandler{}

// APIPushResponse represents the push response data that will be returned to the uniqush client.
type APIPushResponse struct {
	Type           string               `json:"type"`
	Date           int64                `json:"date"`
	SuccessCount   int                  `json:"successCount"`
	FailureCount   int                  `json:"failureCount"`
	DroppedCount   int                  `json:"droppedCount"`
	SuccessDetails []APIResponseDetails `json:"successDetails"`
	FailureDetails []APIResponseDetails `json:"failureDetails"`
	DroppedDetails []APIResponseDetails `json:"droppedDetails"`
}

func newPushResponseHandler(logger log.Logger) *APIPushResponseHandler {
	return &APIPushResponseHandler{
		response: newAPIPushResponse(),
		logger:   logger,
	}
}

func newAPIPushResponse() APIPushResponse {
	return APIPushResponse{
		Type:           "Push",
		Date:           time.Now().Unix(),
		SuccessDetails: make([]APIResponseDetails, 0),
		FailureDetails: make([]APIResponseDetails, 0),
		DroppedDetails: make([]APIResponseDetails, 0),
	}
}

// AddDetailsToHandler will record information about one response (of one or more responses) to an individual push attempt to a psp.
func (handler *APIPushResponseHandler) AddDetailsToHandler(v APIResponseDetails) {
	handler.mutex.Lock()
	if v.Code == UNIQUSH_SUCCESS {
		handler.response.SuccessDetails = append(handler.response.SuccessDetails, v)
		handler.response.SuccessCount++
	} else if v.Code == UNIQUSH_UPDATE_UNSUBSCRIBE || v.Code == UNIQUSH_REMOVE_INVALID_REG {
		handler.response.DroppedDetails = append(handler.response.DroppedDetails, v)
		handler.response.DroppedCount++
	} else {
		handler.response.FailureDetails = append(handler.response.FailureDetails, v)
		handler.response.FailureCount++
	}
	handler.mutex.Unlock()
}

// ToJSON serializes this push response as JSON to send to the client of uniqush-push.
func (handler *APIPushResponseHandler) ToJSON() []byte {
	json, err := json.Marshal(handler.response)
	if err != nil {
		handler.logger.Errorf("Failed to marshal json [%v] as string: %v", handler.response, err)
		return nil
	}
	return json
}

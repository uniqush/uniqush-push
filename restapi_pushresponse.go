package main

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/uniqush/log"
)

type APIPushResponseHandler struct {
	response APIPushResponse
	logger   log.Logger
	mutex    sync.Mutex
}

var _ APIResponseHandler = (*APIPushResponseHandler)(nil)

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

func (self *APIPushResponseHandler) AddDetailsToHandler(v APIResponseDetails) {
	self.mutex.Lock()
	if v.Code == UNIQUSH_SUCCESS {
		self.response.SuccessDetails = append(self.response.SuccessDetails, v)
		self.response.SuccessCount++
	} else if v.Code == UNIQUSH_UPDATE_UNSUBSCRIBE || v.Code == UNIQUSH_REMOVE_INVALID_REG {
		self.response.DroppedDetails = append(self.response.DroppedDetails, v)
		self.response.DroppedCount++
	} else {
		self.response.FailureDetails = append(self.response.FailureDetails, v)
		self.response.FailureCount++
	}
	self.mutex.Unlock()
}

func (self *APIPushResponseHandler) ToJSON() []byte {
	json, err := json.Marshal(self.response)
	if err != nil {
		self.logger.Errorf("Failed to marshal json [%v] as string: %v", self.response, err)
		return nil
	}
	return json
}

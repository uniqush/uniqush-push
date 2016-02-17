package main

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/uniqush/log"
)

type ApiPushResponseHandler struct {
	response ApiPushResponse
	logger   log.Logger
	mutex    sync.Mutex
}

var _ ApiResponseHandler = (*ApiPushResponseHandler)(nil)

type ApiPushResponse struct {
	Type           string               `json:"type"`
	Date           int64                `json:"date"`
	SuccessCount   int                  `json:"successCount"`
	FailureCount   int                  `json:"failureCount"`
	DroppedCount   int                  `json:"droppedCount"`
	SuccessDetails []ApiResponseDetails `json:"successDetails"`
	FailureDetails []ApiResponseDetails `json:"failureDetails"`
	DroppedDetails []ApiResponseDetails `json:"droppedDetails"`
}

func newPushResponseHandler(logger log.Logger) *ApiPushResponseHandler {
	return &ApiPushResponseHandler{
		response: newApiPushResponse(),
		logger:   logger,
	}
}

func newApiPushResponse() ApiPushResponse {
	return ApiPushResponse{
		Type:           "Push",
		Date:           time.Now().Unix(),
		SuccessDetails: make([]ApiResponseDetails, 0),
		FailureDetails: make([]ApiResponseDetails, 0),
		DroppedDetails: make([]ApiResponseDetails, 0),
	}
}

func (self *ApiPushResponseHandler) AddDetailsToHandler(v ApiResponseDetails) {
	self.mutex.Lock()
	if v.Code == UNIQUSH_SUCCESS {
		self.response.SuccessDetails = append(self.response.SuccessDetails, v)
		self.response.SuccessCount += 1
	} else if v.Code == UNIQUSH_UPDATE_UNSUBSCRIBE || v.Code == UNIQUSH_REMOVE_INVALID_REG {
		self.response.DroppedDetails = append(self.response.DroppedDetails, v)
		self.response.DroppedCount += 1
	} else {
		self.response.FailureDetails = append(self.response.FailureDetails, v)
		self.response.FailureCount += 1
	}
	self.mutex.Unlock()
}

func (self *ApiPushResponseHandler) ToJSON() []byte {
	json, err := json.Marshal(self.response)
	if err != nil {
		self.logger.Errorf("Failed to marshal json [%v] as string: %v", self.response, err)
		return nil
	}
	return json
}

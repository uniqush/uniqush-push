package main

import (
	"encoding/json"
	"github.com/uniqush/log"
	"time"
)

const (
	STATUS_SUCCESS = iota
	STATUS_FAILURE
	STATUS_UNKNOWN
)

type ApiSimpleResponseHandler struct {
	response ApiSimpleResponse
	logger   log.Logger
}

var _ ApiResponseHandler = (*ApiSimpleResponseHandler)(nil)

type ApiSimpleResponse struct {
	Type    string             `json:"type"`
	Date    int64              `json:"date"`
	Status  int                `json:"status"`
	Details ApiResponseDetails `json:"details"`
}

func newSimpleResponseHandler(logger log.Logger, apiType string) *ApiSimpleResponseHandler {
	return &ApiSimpleResponseHandler{
		response: newApiSimpleResponse(apiType),
		logger:   logger,
	}
}

func newApiSimpleResponse(apiType string) ApiSimpleResponse {
	return ApiSimpleResponse{
		Type:   apiType,
		Date:   time.Now().Unix(),
		Status: STATUS_UNKNOWN,
	}
}

func (self *ApiSimpleResponseHandler) AddDetailsToHandler(v ApiResponseDetails) {
	if v.Code == UNIQUSH_SUCCESS {
		self.response.Status = STATUS_SUCCESS
	} else {
		self.response.Status = STATUS_FAILURE
	}
	self.response.Details = v
}

func (self *ApiSimpleResponseHandler) ToJSON() []byte {
	json, err := json.Marshal(self.response)
	if err != nil {
		self.logger.Errorf("Failed to marshal json [%v] as string: %v", self.response, err)
		return nil
	}
	return json
}

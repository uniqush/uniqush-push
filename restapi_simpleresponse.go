package main

import (
	"encoding/json"
	"github.com/uniqush/log"
	"time"
)

// status codes for REST APIs with simple responses
const (
	STATUS_SUCCESS = iota
	STATUS_FAILURE
	STATUS_UNKNOWN
)

type APISimpleResponseHandler struct {
	response APISimpleResponse
	logger   log.Logger
}

var _ APIResponseHandler = (*APISimpleResponseHandler)(nil)

type APISimpleResponse struct {
	Type    string             `json:"type"`
	Date    int64              `json:"date"`
	Status  int                `json:"status"`
	Details APIResponseDetails `json:"details"`
}

func newSimpleResponseHandler(logger log.Logger, apiType string) *APISimpleResponseHandler {
	return &APISimpleResponseHandler{
		response: newAPISimpleResponse(apiType),
		logger:   logger,
	}
}

func newAPISimpleResponse(apiType string) APISimpleResponse {
	return APISimpleResponse{
		Type:   apiType,
		Date:   time.Now().Unix(),
		Status: STATUS_UNKNOWN,
	}
}

func (handler *APISimpleResponseHandler) AddDetailsToHandler(v APIResponseDetails) {
	if v.Code == UNIQUSH_SUCCESS {
		handler.response.Status = STATUS_SUCCESS
	} else {
		handler.response.Status = STATUS_FAILURE
	}
	handler.response.Details = v
}

func (handler *APISimpleResponseHandler) ToJSON() []byte {
	json, err := json.Marshal(handler.response)
	if err != nil {
		handler.logger.Errorf("Failed to marshal json [%v] as string: %v", handler.response, err)
		return nil
	}
	return json
}

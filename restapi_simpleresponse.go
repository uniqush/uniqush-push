package main

import (
	"encoding/json"
	"github.com/uniqush/log"
	"time"
)

// status codes for REST APIs with simple responses
const (
	StatusSuccess = iota
	StatusFailure
	StatusUnknown
)

// APISimpleResponseHandler is a handler that expects exactly one response to be added
type APISimpleResponseHandler struct {
	response APISimpleResponse
	logger   log.Logger
}

var _ APIResponseHandler = &APISimpleResponseHandler{}

// APISimpleResponse serializes an API response containing a single response details object.
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
		Status: StatusUnknown,
	}
}

// AddDetailsToHandler will set the only response's status and details.
func (handler *APISimpleResponseHandler) AddDetailsToHandler(v APIResponseDetails) {
	if v.Code == UNIQUSH_SUCCESS {
		handler.response.Status = StatusSuccess
	} else {
		handler.response.Status = StatusFailure
	}
	handler.response.Details = v
}

// ToJSON will return the serialization of the only response.
func (handler *APISimpleResponseHandler) ToJSON() []byte {
	json, err := json.Marshal(handler.response)
	if err != nil {
		handler.logger.Errorf("Failed to marshal json [%v] as string: %v", handler.response, err)
		return nil
	}
	return json
}

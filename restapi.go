/*
 * Copyright 2011 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"strings"
	"fmt"
	"github.com/uniqush/log"
	. "github.com/uniqush/uniqush-push/push"
	"io"
	"net/http"
	"regexp"
)

type RestAPI struct {
	psm       *PushServiceManager
	logOutput io.Writer
	logLevel  int
	version   string
	backend   *PushBackEnd
}

func NewRestAPI(psm *PushServiceManager, logOutput io.Writer, logLevel int, version string, backend *PushBackEnd) *RestAPI {
	ret := new(RestAPI)
	ret.psm = psm
	ret.logOutput = logOutput
	ret.logLevel = logLevel
	ret.version = version
	ret.backend = backend
	return ret
}

const (
	ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL    = "/addpsp"
	REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL = "/rmpsp"
	ADD_DELIVERY_POINT_TO_SERVICE_URL           = "/subscribe"
	REMOVE_DELIVERY_POINT_FROM_SERVICE_URL      = "/unsubscribe"
	PUSH_NOTIFICATION_URL                       = "/push"
	STOP_PROGRAM_URL                            = "/stop"
	VERSION_INFO_URL                            = "/version"
)

var validServicePattern *regexp.Regexp
var validSubscriberPattern *regexp.Regexp

func init() {
	var err error
	validServicePattern, err = regexp.Compile("^[a-zA-z\\.0-9-_]+$")
	if err != nil {
		validServicePattern = nil
	}
	validSubscriberPattern, err = regexp.Compile("^[a-zA-z\\.0-9-_]+$")
	if err != nil {
		validSubscriberPattern = nil
	}
}

func validateSubscribers(subs []string) error {
	if validSubscriberPattern != nil {
		for _, sub := range subs {
			if !validSubscriberPattern.MatchString(sub) {
				return fmt.Errorf("invalid subscriber name: %s. Accept charaters: a-z, A-Z, 0-9, -, _ or .", sub)
			}
		}
	}
	return nil
}

func validateService(service string) error {
	if validServicePattern != nil {
		if !validServicePattern.MatchString(service) {
			return fmt.Errorf("invalid service name: %s. Accept charaters: a-z, A-Z, 0-9, -, _ or .", service)
		}
	}
	return nil
}

func getSubscribersFromMap(kv map[string]string, validate bool) (subs []string, err error) {
	var v string
	var ok bool
	if v, ok = kv["subscriber"]; !ok {
		if v, ok = kv["subscribers"]; !ok {
			err = fmt.Errorf("NoSubscriber")
			return
		}
	}
	subs = strings.Split(v, ",")
	if validate {
		err = validateSubscribers(subs)
		if err != nil {
			subs = nil
			return
		}
	}
	return
}

func getServiceFromMap(kv map[string]string, validate bool) (service string, err error) {
	var ok bool
	if service, ok = kv["service"]; !ok {
		err = fmt.Errorf("NoService")
		return
	}
	if validate {
		err = validateService(service)
		if err != nil {
			service = ""
			return
		}
	}
	return
}

func (self *RestAPI) changePushServiceProvider(kv map[string]string, logger *log.Logger, add bool) {
	psp, err := self.psm.BuildPushServiceProviderFromMap(kv)
	if err != nil {
		logger.Errorf("Cannot build push service provider: %v", err)
		return
	}
	defer psp.Recycle()
	service, err := getServiceFromMap(kv, true)
	if err != nil {
		logger.Errorf("Cannot get service name: %v", service)
	}
	if add {
		err = self.backend.AddPushServiceProvider(service, psp)
	} else {
		err = self.backend.RemovePushServiceProvider(service, psp)
	}
	if err != nil {
		logger.Errorf("Cannot add the push service provider: %v\n", err)
	}
	return
}

func (self RestAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case VERSION_INFO_URL:
		fmt.Fprintf(w, "%v\r\n", self.version)
		return
	case STOP_PROGRAM_URL:
		fmt.Fprintf(w, "Stop\r\n")
		// TODO do something
		return
	}
	r.ParseForm()
	kv := make(map[string]string, len(r.Form))
	for k, v := range r.Form {
		if len(v) > 0 {
			kv[k] = v[0]
		}
	}
	writer := io.MultiWriter(self.logOutput, w)
	switch r.URL.Path {
	case ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL:
		logger := log.NewLogger(writer, "[AddPushServiceProvider]", self.logLevel)
		self.changePushServiceProvider(kv, logger, true)
	case REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL:
		logger := log.NewLogger(writer, "[RemovePushServiceProvider]", self.logLevel)
		self.changePushServiceProvider(kv, logger, false)
	}
}

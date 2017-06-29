/*
 * Copyright 2011-2013 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package binary_api

// This file implements a goroutine that periodically connects to apple's feedback servers, and forwards unsubscribe updates on a channel of push.PushErrors

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/uniqush/cache"
	"github.com/uniqush/uniqush-push/push"
)

const (
	// in Minutes
	feedbackCheckPeriod int = 10
)

func connectFeedback(psp *push.PushServiceProvider) (net.Conn, error) {
	cert, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if err != nil {
		return nil, push.NewBadPushServiceProviderWithDetails(psp, err.Error())
	}

	conf := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: false,
	}

	if skip, ok := psp.VolatileData["skipverify"]; ok {
		if skip == "true" {
			conf.InsecureSkipVerify = true
		}
	}

	addr := "feedback.sandbox.push.apple.com:2196"
	if psp.VolatileData["addr"] == "gateway.push.apple.com:2195" {
		addr = "feedback.push.apple.com:2196"
	} else if psp.VolatileData["addr"] == "gateway.sandbox.push.apple.com:2195" {
		addr = "feedback.sandbox.push.apple.com:2196"
	} else {
		ae := strings.Split(psp.VolatileData["addr"], ":")
		addr = fmt.Sprintf("%v:2196", ae[0])
	}
	tlsconn, err := tls.Dial("tcp", addr, conf)
	if err != nil {
		return nil, err
	}
	err = tlsconn.Handshake()
	if err != nil {
		return nil, err
	}
	return tlsconn, nil
}

func receiveFeedback(psp *push.PushServiceProvider) []string {
	conn, err := connectFeedback(psp)
	if conn == nil || err != nil {
		return nil
	}
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()

	ret := make([]string, 0, 1)
	for {
		var unsubTime uint32
		var tokenLen uint16

		err := binary.Read(conn, binary.BigEndian, &unsubTime)
		if err != nil {
			return ret
		}
		err = binary.Read(conn, binary.BigEndian, &tokenLen)
		if err != nil {
			return ret
		}
		devtoken := make([]byte, int(tokenLen))

		n, err := io.ReadFull(conn, devtoken)
		if err != nil {
			return ret
		}
		if n != int(tokenLen) {
			return ret
		}

		devtokenstr := hex.EncodeToString(devtoken)
		devtokenstr = strings.ToLower(devtokenstr)
		ret = append(ret, devtokenstr)
	}
}

func processFeedback(psp *push.PushServiceProvider, dpCache *cache.Cache, errChan chan<- push.PushError) {
	keys := receiveFeedback(psp)
	for _, token := range keys {
		dpif := dpCache.Delete(token)
		if dpif == nil {
			continue
		}
		if dp, ok := dpif.(*push.DeliveryPoint); ok {
			err := push.NewUnsubscribeUpdate(psp, dp)
			errChan <- err
		}
	}
}

// feedbackChecker periodically connects to the APNS feedback servers to get a list of unsubscriptions.
func feedbackChecker(psp *push.PushServiceProvider, dpCache *cache.Cache, errChan chan<- push.PushError) {
	for {
		time.Sleep(time.Duration(feedbackCheckPeriod) * time.Minute)
		processFeedback(psp, dpCache, errChan)
	}
}

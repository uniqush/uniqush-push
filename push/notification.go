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

package push

import (
	"encoding/json"
)

type Notification struct {
	Data map[string]string
}

func (self *Notification) String() string {
	ret, _ := json.Marshal(self.Data)
	return string(ret)
}
func NewEmptyNotification() *Notification {
	n := new(Notification)
	n.Data = make(map[string]string, 10)
	return n
}

func (n *Notification) Clone() *Notification {
	ret := new(Notification)
	ret.Data = make(map[string]string, len(n.Data))
	for k, v := range n.Data {
		ret.Data[k] = v
	}
	return ret
}

func (n *Notification) IsEmpty() bool {
	if len(n.Data) == 0 {
		return true
	}
	return false
}

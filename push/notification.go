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

func (n *Notification) String() string {
	ret, _ := json.Marshal(n.Data)
	return string(ret)
}

// NewEmptyNotification returns an initialized notification with no data.
func NewEmptyNotification() *Notification {
	return &Notification{
		Data: make(map[string]string, 10),
	}
}

// Clone returns a clone of this notification
func (n *Notification) Clone() *Notification {
	Data := make(map[string]string, len(n.Data))
	for k, v := range n.Data {
		Data[k] = v
	}
	return &Notification{Data: Data}
}

// IsEmpty returns true if there are fields in this notification
func (n *Notification) IsEmpty() bool {
	return len(n.Data) == 0
}

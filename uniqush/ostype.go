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

package uniqush

import (
    "strings"
)

const (
    OSTYPE_UNKOWN = iota
    OSTYPE_ANDROID
    OSTYPE_IOS
    OSTYPE_WP
    OSTYPE_BLKBERRY
)

/* XXX do we need to add version info? */
type OSType struct {
    id int
}

var (
    OS_ANDROID OSType
    OS_IOS OSType
)

func init() {
    OS_ANDROID = OSType{OSTYPE_ANDROID}
    OS_IOS = OSType{OSTYPE_IOS}
}

func (o *OSType) OSName() string {
    switch (o.id) {
    case OSTYPE_ANDROID:
        return "Android"
    case OSTYPE_IOS:
        return "iOS"
    case OSTYPE_WP:
        return "Windows Phone"
    case OSTYPE_BLKBERRY:
        return "BlackBerry"
    }
    return "Unknown"
}

func OSNameToID(os string) int {
    switch (strings.ToLower(os)) {
    case "android":
        return OSTYPE_ANDROID
    case "ios":
        return OSTYPE_IOS
    case "windowsphone":
        return OSTYPE_WP
    case "blackberry":
        return OSTYPE_BLKBERRY
    }
    return OSTYPE_UNKOWN
}

func (o *OSType) OSID() int {
    return o.id
}

func (o *OSType) String() string {
    return o.OSName()
}



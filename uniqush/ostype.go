/*
 *  Uniqush by Nan Deng
 *  Copyright (C) 2010 Nan Deng
 *
 *  This software is free software; you can redistribute it and/or
 *  modify it under the terms of the GNU Lesser General Public
 *  License as published by the Free Software Foundation; either
 *  version 3.0 of the License, or (at your option) any later version.
 *
 *  This software is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 *  Lesser General Public License for more details.
 *
 *  You should have received a copy of the GNU Lesser General Public
 *  License along with this software; if not, write to the Free Software
 *  Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA  02111-1307  USA
 *
 *  Nan Deng <monnand@gmail.com>
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

/* TODO add version info */
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



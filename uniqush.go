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

package main

import (
    "uniqush"
    "os"
    "fmt"
    "flag"
)

var conf = flag.String("config", "/etc/uniqush/uniqush.conf", "Config file path")

func main() {
    //conf := "/etc/uniqush/uniqush.conf"
    flag.Parse()
    unisys, err := uniqush.LoadUniqushSystem(*conf)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Fatal Error: %v\n", err)
        os.Exit(-1)
    }
    unisys.Run()
}


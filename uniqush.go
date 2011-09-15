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
    "github.com/monnand/uniqush/uniqush"
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


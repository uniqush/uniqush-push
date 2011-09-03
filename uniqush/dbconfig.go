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
    "os"
    //"bufio"
    "io/ioutil"
    "json"
    "fmt"
)

type DatabaseConfig struct {
    Engine string
    Name string
    User string
    Password string
    Host string
    Port int
    CacheSize int

    /* dump the dirty data to db every EverySec seconds,
     * if there are more than LeastDirty dirty items
     */
    EverySec int64
    LeastDirty int
}

const (
    DEFAULT_DBCONF_FILE string = "/etc/uniqush.conf"
)

func LoadDatabaseConfig(filename string) (conf *DatabaseConfig, err os.Error) {
    if len(filename) == 0 {
        filename = DEFAULT_DBCONF_FILE
    }
    content, e := ioutil.ReadFile(filename)
    if e != nil {
        err = e
        conf = nil
        return
    }
    /*
    str := string(content)
    fmt.Print(str, "\n")
    */
    conf = new(DatabaseConfig)
    err = json.Unmarshal(content, conf)
    return
}

func (c *DatabaseConfig) String() string {
    ret := fmt.Sprintf("engine: %v;\nname: %v;\nuser: %v;\npassowrd: %v;\nhost: %v\nport: %d\n",
                       c.Engine, c.Name, c.User, c.Password, c.Host, c.Port)

    return ret
}


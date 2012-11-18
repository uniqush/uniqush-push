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

package db

import (
	"fmt"
	. "github.com/uniqush/uniqush-push/push"
)

type DatabaseConfig struct {
	Engine    string
	Name      string
	User      string
	Password  string
	Host      string
	Port      int
	CacheSize int

	/* dump the dirty data to db every EverySec seconds,
	 * if there are more than LeastDirty dirty items
	 */
	EverySec   int64
	LeastDirty int

	PushServiceManager *PushServiceManager
}

/*
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
    conf = new(DatabaseConfig)
    err = json.Unmarshal(content, conf)
    return
}
*/

func (c *DatabaseConfig) String() string {
	ret := fmt.Sprintf("engine: %v;\nname: %v;\nuser: %v;\npassowrd: %v;\nhost: %v\nport: %d\n",
		c.Engine, c.Name, c.User, c.Password, c.Host, c.Port)

	return ret
}

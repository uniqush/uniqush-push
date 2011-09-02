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


package main

import (
    "uniqush"
    "log"
    "os"
)

func main() {
    logger := log.New(os.Stdout, "[uniqush][frontend][web] ", log.LstdFlags)
    ch := make(chan *uniqush.Request)
    f := uniqush.NewWebFrontEnd(ch, logger, "localhost:9898")
    ew := uniqush.NewEventWriter(os.Stderr)
    f.SetEventWriter(ew)

    logger = log.New(os.Stdout, "[uniqush][backend] ", log.LstdFlags)
    b := uniqush.NewUniqushBackEnd(ch, logger)


    logger = log.New(os.Stdout, "[uniqush][actionprinter] ", log.LstdFlags)
    p := uniqush.NewActionPrinter(logger)

    for i := 0; i < uniqush.NR_ACTIONS; i++ {
        b.SetProcessor(i, p)
    }

    c := new(uniqush.DatabaseConfig)
    c.Port = -1
    c.Engine = "redis"
    c.Name = "6"
    c.EverySec = 600
    c.LeastDirty = 10
    c.CacheSize = 10

    dbf := uniqush.NewDatabaseFrontDesk(c)
    logger = log.New(os.Stdout, "[uniqush][backend][AddPushServiceProviderProcessor] ", log.LstdFlags)
    p = uniqush.NewAddPushServiceProviderProcessor(logger, ew, dbf)
    b.SetProcessor(uniqush.ACTION_ADD_PUSH_SERVICE_PROVIDER, p)


    go f.Run()
    go b.Run()
    <-make(chan int)
}


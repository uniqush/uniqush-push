package main

import (
    "uniqush"
    "log"
    "os"
    "fmt"
)

func main() {
    logger := log.New(os.Stdout, "[uniqush][frontend][web] ", log.LstdFlags)
    ch := make(chan *uniqush.Request)
    stopch := make(chan bool)
    f := uniqush.NewWebFrontEnd(ch, logger, "localhost:9898")
    f.SetStopChannel(stopch)

    //ew := uniqush.NewEventWriter(os.Stderr)
    ew := uniqush.NewEventWriter(&uniqush.NullWriter{})
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
    c.Name = "0"
    c.EverySec = 600
    c.LeastDirty = 10
    c.CacheSize = 10

    dbf := uniqush.NewDatabaseFrontDesk(c)

    logger = log.New(os.Stdout, "[uniqush][backend][AddPushServiceProviderProcessor] ", log.LstdFlags)
    p = uniqush.NewAddPushServiceProviderProcessor(logger, ew, dbf)
    b.SetProcessor(uniqush.ACTION_ADD_PUSH_SERVICE_PROVIDER, p)

    logger = log.New(os.Stdout, "[uniqush][backend][RemovePushServiceProviderProcessor] ", log.LstdFlags)
    p = uniqush.NewRemovePushServiceProviderProcessor(logger, ew, dbf)
    b.SetProcessor(uniqush.ACTION_REMOVE_PUSH_SERVICE_PROVIDER, p)

    logger = log.New(os.Stdout, "[uniqush][backend][Subscribe] ", log.LstdFlags)
    p = uniqush.NewSubscribeProcessor(logger, ew, dbf)
    b.SetProcessor(uniqush.ACTION_SUBSCRIBE, p)

    logger = log.New(os.Stdout, "[uniqush][backend][Unsubscribe] ", log.LstdFlags)
    p = uniqush.NewUnsubscribeProcessor(logger, ew, dbf)
    b.SetProcessor(uniqush.ACTION_UNSUBSCRIBE, p)

    logger = log.New(os.Stdout, "[uniqush][backend][Push] ", log.LstdFlags)
    p = uniqush.NewPushProcessor(logger, ew, dbf)

    b.SetProcessor(uniqush.ACTION_PUSH, p)

    go f.Run()
    go b.Run()

    stop := <-stopch;
    for !stop {
        fmt.Printf("What?!\n")
        stop = <-stopch;
    }
    fmt.Printf("Flush cache!\n")
    dbf.FlushCache()
}


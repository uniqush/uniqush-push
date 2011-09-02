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


    logger = log.New(os.Stdout, "[uniqush][backend] ", log.LstdFlags)
    b := uniqush.NewUniqushBackEnd(ch, logger)


    logger = log.New(os.Stdout, "[uniqush][actionprinter] ", log.LstdFlags)
    p := uniqush.NewActionPrinter(logger)

    for i := 0; i < uniqush.NR_ACTIONS; i++ {
        b.SetProcessor(i, p)
    }
    go f.Run()
    go b.Run()
    <-make(chan int)
}


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
)

func main() {
    logger := uniqush.NewLogger(os.Stdout, "[uniqush][frontend][web]", uniqush.LOGLEVEL_DEBUG)
    ch := make(chan *uniqush.Request)
    stopch := make(chan bool)
    f := uniqush.NewWebFrontEnd(ch, logger, "localhost:9898")
    f.SetStopChannel(stopch)

    //ew := uniqush.NewEventWriter(os.Stderr)
    ew := uniqush.NewEventWriter(&uniqush.NullWriter{})
    f.SetEventWriter(ew)

    logger = uniqush.NewLogger(os.Stdout, "[uniqush][backend]", uniqush.LOGLEVEL_DEBUG)
    b := uniqush.NewUniqushBackEnd(ch, logger)

    logger = uniqush.NewLogger(os.Stdout, "[uniqush][actionprinter]", uniqush.LOGLEVEL_DEBUG)
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

    logger = uniqush.NewLogger(os.Stdout, "[uniqush][backend][AddPushServiceProviderProcessor]", uniqush.LOGLEVEL_DEBUG)
    p = uniqush.NewAddPushServiceProviderProcessor(logger, ew, dbf)
    b.SetProcessor(uniqush.ACTION_ADD_PUSH_SERVICE_PROVIDER, p)

    logger = uniqush.NewLogger(os.Stdout, "[uniqush][backend][RemovePushServiceProviderProcessor]", uniqush.LOGLEVEL_DEBUG)
    p = uniqush.NewRemovePushServiceProviderProcessor(logger, ew, dbf)
    b.SetProcessor(uniqush.ACTION_REMOVE_PUSH_SERVICE_PROVIDER, p)

    logger = uniqush.NewLogger(os.Stdout, "[uniqush][backend][Subscribe]", uniqush.LOGLEVEL_DEBUG)
    p = uniqush.NewSubscribeProcessor(logger, ew, dbf)
    b.SetProcessor(uniqush.ACTION_SUBSCRIBE, p)

    logger = uniqush.NewLogger(os.Stdout, "[uniqush][backend][Unsubscribe]", uniqush.LOGLEVEL_DEBUG)
    p = uniqush.NewUnsubscribeProcessor(logger, ew, dbf)
    b.SetProcessor(uniqush.ACTION_UNSUBSCRIBE, p)

    logger = uniqush.NewLogger(os.Stdout, "[uniqush][backend][Push]", uniqush.LOGLEVEL_DEBUG)
    p = uniqush.NewPushProcessor(logger, ew, dbf, ch)

    b.SetProcessor(uniqush.ACTION_PUSH, p)

    go f.Run()
    go b.Run()

    <-stopch;
    fmt.Printf("Flush cache!\n")
    dbf.FlushCache()
}


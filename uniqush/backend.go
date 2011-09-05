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
    "log"
)

type UniqushBackEndIf interface {
    SetChannel(ch <-chan *Request)
    SetLogger(logger *log.Logger)
    SetProcessor(action int, proc RequestProcessor)
    Run()
}

type UniqushBackEnd struct {
    procs []RequestProcessor
    ch <-chan *Request
    logger *log.Logger
}

func NewUniqushBackEnd(ch chan *Request, logger *log.Logger) UniqushBackEndIf {
    b := new(UniqushBackEnd)
    b.ch = ch
    b.logger = logger
    return b
}

func (b *UniqushBackEnd) SetChannel(ch <-chan *Request) {
    b.ch = ch
}

func (b *UniqushBackEnd) SetLogger(logger *log.Logger) {
    b.logger = logger
}

func (b *UniqushBackEnd) SetProcessor(action int, proc RequestProcessor) {
    if len(b.procs) < NR_ACTIONS {
        a := make([]RequestProcessor, NR_ACTIONS, NR_ACTIONS)
        copy(a, b.procs)
        b.procs = a
    }
    if action < 0 || action >= NR_ACTIONS {
        return
    }
    b.procs[action] = proc
}

func (b *UniqushBackEnd) Run() {
    if len(b.procs) < NR_ACTIONS {
        return
    }
    for {
        r := <-b.ch

        if r.Action < 0 || r.Action >= NR_ACTIONS {
            continue
        }
        p := b.procs[r.Action]
        go p.Process(r)
    }
}


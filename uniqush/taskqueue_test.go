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
    "testing"
    "fmt"
    "time"
    "os"
)

type ExpBackoffTask struct {
    ch chan<- Task
    backofftime int64
    TaskTime
}

func (t *ExpBackoffTask) Run(curtime int64) {
    if t.backofftime >= 32E9 {
        t.ch <- nil
        os.Exit(0)
    }
    t.backofftime = t.backofftime << 1
    t.execTime = curtime + t.backofftime
    fmt.Printf("I run @ %d, I will wait %d nanoseconds to run @ %ds %dns\n",
               curtime, t.backofftime, t.ExecTime()/1E9, t.execTime)
    t.ch <- t
}

func TestTaskQueue(t *testing.T) {
    task := new(ExpBackoffTask)
    ch := make(chan Task)
    task.ch = ch
    task.backofftime = 1E9
    task.execTime = time.Nanoseconds() + 1E9
    q := NewTaskQueue(ch)

    go q.Run()
    ch <- task

    <-make(chan int)
}

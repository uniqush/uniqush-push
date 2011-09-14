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

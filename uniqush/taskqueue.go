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
    "github.com/petar/GoLLRB/llrb"
    "time"
)

type Task interface {
    Run(time int64)
    ExecTime() int64
}

type TaskTime struct {
    execTime int64
}

func (t *TaskTime) ExecTime() int64 {
    return t.execTime
}

type TaskQueue struct {
    tree *llrb.Tree
    ch <-chan Task
    waitTime int64
}

const (
    MAX_TIME int64 = 0x0FFFFFFFFFFFFFFF
)

func taskBefore (a, b interface{}) bool{
    return a.(Task).ExecTime() < b.(Task).ExecTime()
}

func NewTaskQueue(ch <-chan Task) *TaskQueue {
    ret := new(TaskQueue)
    ret.tree = llrb.New(taskBefore)
    ret.ch = ch
    ret.waitTime = MAX_TIME
    return ret
}

func (t *TaskQueue) Run() {
    for {
        select {
        case task := <-t.ch:
            if task == nil {
                return
            }
            /*
            fmt.Printf("I received a task. Current Time %d, it ask me to run it at %d\n",
                        time.Nanoseconds()/1E9, task.ExecTime()/1E9)
                        */
            if task.ExecTime() <= time.Nanoseconds() {
                go task.Run(time.Nanoseconds())
                continue
            }
            t.tree.ReplaceOrInsert(task)
            x := t.tree.Min()
            if x == nil {
                t.waitTime = MAX_TIME
                continue
            }
            task = x.(Task)
            t.waitTime = task.ExecTime() - time.Nanoseconds()
        case <-time.After(t.waitTime):
            x := t.tree.Min()
            if x == nil {
                t.waitTime = MAX_TIME
                continue
            }
            task := x.(Task)
            /*
            fmt.Printf("Current Time %d, a task ask me to run it at %d\n",
                        time.Nanoseconds()/1E9, task.ExecTime()/1E9)
                        */
            for task.ExecTime() <= time.Nanoseconds() {
                go task.Run(time.Nanoseconds())
                t.tree.DeleteMin()
                x = t.tree.Min()
                if x == nil {
                    t.waitTime = MAX_TIME
                    task = nil
                    break
                }
                /*
                fmt.Printf("Current Time %d, a task ask me to run it at %d\n",
                        time.Nanoseconds()/1E9, task.ExecTime()/1E9)
                        */
                task = x.(Task)
            }
            if task != nil {
                t.waitTime = task.ExecTime() - time.Nanoseconds()
            }
        }
    }
}


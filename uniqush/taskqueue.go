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
    ch <-chan *Task
    waitTime int64
}

const (
    MAX_TIME int64 = 0x0FFFFFFFFFFFFFFF
)

func taskBefore (a, b interface{}) bool{
    return a.(Task).ExecTime() < b.(Task).ExecTime()
}

func NewTaskQueue(ch <-chan *Task) *TaskQueue {
    ret := new(TaskQueue)
    ret.tree = llrb.New(taskBefore)
    ret.ch = ch
    ret.waitTime = MAX_TIME
    return ret
}

func (t *TaskQueue) Run() {
    for {
        select {
        case action := <-t.ch:
            if action == nil {
                return
            }
            task := *action
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


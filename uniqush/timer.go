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

type TimerActionProcessor interface {
    Act(time int64, data interface{})
}

type NullTimerActionProcessor struct {}

func (p *NullTimerActionProcessor) Act(time int64, data interface{}) {
}

type Timer struct {
    tree *llrb.Tree
    actionProcessor TimerActionProcessor
    ch <-chan *TimerAction
    waitTime int64
}

type TimerAction struct {
    execTime int64
    data interface{}
}

const (
    MAX_TIME int64 = 0x0FFFFFFFFFFFFFFF
)

func NewTimerAction(execTime int64, data interface{}) *TimerAction {
    ret := new(TimerAction)
    ret.execTime = execTime
    ret.data = data
    return ret
}

func actionLess(a, b interface{}) bool{
    return a.(*TimerAction).execTime < a.(*TimerAction).execTime
}

func NewTimer(p TimerActionProcessor, ch <-chan *TimerAction) *Timer {
    ret := new(Timer)
    ret.tree = llrb.New(actionLess)
    ret.actionProcessor = p
    ret.ch = ch
    ret.waitTime = MAX_TIME
    return ret
}

func (t *Timer) Run() {
    for {
        select {
        case action := <-t.ch:
            if action == nil {
                return
            }
            t.tree.ReplaceOrInsert(action)
            if action.execTime <= time.Nanoseconds() + 10 {
                t.actionProcessor.Act(time.Nanoseconds(), action.data)
                continue
            }
            x := t.tree.Min()
            if x == nil {
                t.waitTime = MAX_TIME
                continue
            }
            action = x.(*TimerAction)
            t.waitTime = action.execTime - time.Nanoseconds()
        case <-time.After(t.waitTime):
            x := t.tree.Min()
            if x == nil {
                t.waitTime = MAX_TIME
                continue
            }
            action := x.(*TimerAction)
            for action.execTime <= time.Nanoseconds() {
                t.actionProcessor.Act(time.Nanoseconds(), action.data)
                t.tree.DeleteMin()
                x := t.tree.Min()
                if x == nil {
                    t.waitTime = MAX_TIME
                    continue
                }
                action = x.(*TimerAction)
            }
            t.waitTime = action.execTime - time.Nanoseconds()
        }
    }
}


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

package main

import (
	"github.com/uniqush/log"
	"sync"
)

type PushBackEnd interface {
	SetChannel(ch <-chan *Request)
	SetLogger(logger *uniqushlog.Logger)
	SetProcessor(action int, proc RequestProcessor)
	Run()
	Finalize()
}

type pushBackEnd struct {
	procs  []RequestProcessor
	ch     <-chan *Request
	logger *uniqushlog.Logger
	wg     sync.WaitGroup
}

func NewPushBackEnd(ch chan *Request, logger *uniqushlog.Logger) PushBackEnd {
	b := new(pushBackEnd)
	b.ch = ch
	b.logger = logger
	return b
}

func (b *pushBackEnd) SetChannel(ch <-chan *Request) {
	b.ch = ch
}

func (b *pushBackEnd) SetLogger(logger *uniqushlog.Logger) {
	b.logger = logger
}

func (b *pushBackEnd) SetProcessor(action int, proc RequestProcessor) {
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

func (b *pushBackEnd) Finalize() {
	b.wg.Wait()
}

func (b *pushBackEnd) Run() {
	if len(b.procs) < NR_ACTIONS {
		return
	}
	for r := range b.ch {
		if r.Action < 0 || r.Action >= NR_ACTIONS {
			continue
		}
		p := b.procs[r.Action]
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			p.Process(r)
		}()
	}
}

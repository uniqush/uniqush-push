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

package push

type UniqushBackEndIf interface {
	SetChannel(ch <-chan *Request)
	SetLogger(logger *Logger)
	SetProcessor(action int, proc RequestProcessor)
	Run()
}

type UniqushBackEnd struct {
	procs  []RequestProcessor
	ch     <-chan *Request
	logger *Logger
}

func NewUniqushBackEnd(ch chan *Request, logger *Logger) UniqushBackEndIf {
	b := new(UniqushBackEnd)
	b.ch = ch
	b.logger = logger
	return b
}

func (b *UniqushBackEnd) SetChannel(ch <-chan *Request) {
	b.ch = ch
}

func (b *UniqushBackEnd) SetLogger(logger *Logger) {
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
	for r := range b.ch {
		if r.Action < 0 || r.Action >= NR_ACTIONS {
			continue
		}
		p := b.procs[r.Action]
		go p.Process(r)
	}
}

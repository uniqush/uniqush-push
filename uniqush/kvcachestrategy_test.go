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
)

func convert2string(s []int) []string {
    ret := make([]string, 0, len(s))
    for _, k := range s {
        str := fmt.Sprintf("%d", k)
        ret = append(ret, str)
    }
    return ret
}

func same(a, b []string) bool {
    if len(a) != len(b) {
        return false
    }
    for i := 0; i < len(a); i++ {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

func TestLRUStrategy(t *testing.T) {
    s := NewLRUPeriodFlushStrategy(3, 100, 0)
    allkeys := make([]string, 0, 10)
    var ob []string

    fmt.Print("Start LRU Strategy test ....\t")
    for i := 0; i < 10; i++ {
        str := fmt.Sprint(i)
        allkeys = append(allkeys, str)
        s.Added(str)
    }

    ob = s.GetObsoleted()

    shouldbe := []int{0,1,2,3,4,5,6}
    b := convert2string(shouldbe)
    if !same(b, ob) {
        t.Errorf("case 1 - should be %v, but %v\n", shouldbe, ob)
    }

    for _, k := range ob {
        s.Removed(k)
    }

    for i := 0; i < 5; i++ {
        key := fmt.Sprintf("%d", i)
        allkeys = append(allkeys, key)
        s.Hit(key)
    }

    shouldbe = []int{7,8,9,0,1}
    ob = s.GetObsoleted()
    if !same(convert2string(shouldbe), ob) {
        t.Errorf("case 2 - should be %v, but %v\n", shouldbe, ob)
    }
    fmt.Print("OK\n")
}


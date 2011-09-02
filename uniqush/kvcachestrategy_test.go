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


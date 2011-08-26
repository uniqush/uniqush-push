package uniqush

import (
    "testing"
)

func TestNewNotification(t *testing.T) {
    data := make(map[string]string)
    data["usr1"] = "10"
    n := NewNotification("hello", data)
    if n.badge != -1 {
        t.Errorf("badge wrong! %d", n.badge)
    }
}

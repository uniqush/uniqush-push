package uniqush

import (
    "testing"
    "fmt"
)

func TestDatabaseConfig(t *testing.T) {
    fmt.Print("Start database config test...\n")
    conf, err := LoadDatabaseConfig("")
    if err != nil {
        t.Errorf("Error: %v\n", err)
    }
    fmt.Print(conf, "\n")
    fmt.Print("OK\n")
}

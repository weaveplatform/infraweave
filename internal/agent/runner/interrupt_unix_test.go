//go:build unix

package runner

import (
	"os"
	"os/signal"
	"testing"
	"time"
)

func TestUnitInterruptSendsSIGINT(t *testing.T) {
	got := make(chan os.Signal, 1)
	signal.Notify(got, os.Interrupt)
	defer signal.Stop(got)
	if err := Interrupt(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no SIGINT arrived")
	}
	if err := Interrupt(-1 << 30); err == nil {
		t.Fatal("interrupted a process that does not exist")
	}
}

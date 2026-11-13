// Command forward joins the run containers of a @docker scenario to the
// relay and the object store on a host whose Docker daemon runs in a
// virtual machine, as Docker Desktop's does. Its arguments are pairs: it
// listens on the first of each, a loopback address in the network
// namespace the run containers share, and copies each connection to and
// from the second, a port of the host as host.docker.internal names it.
package main

import (
	"io"
	"net"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 || len(args)%2 != 0 {
		os.Exit(2)
	}
	lns := make([]net.Listener, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		ln, err := net.Listen("tcp", args[i])
		if err != nil {
			os.Exit(1)
		}
		lns = append(lns, ln)
	}
	done := make(chan struct{})
	for i, ln := range lns {
		go func() {
			serve(ln, args[2*i+1])
			close(done)
		}()
	}
	<-done
	os.Exit(1)
}

// serve copies each connection ln accepts to and from to, until ln fails.
func serve(ln net.Listener, to string) {
	for {
		in, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer in.Close()
			out, err := net.Dial("tcp", to)
			if err != nil {
				return
			}
			defer out.Close()
			go func() {
				_, _ = io.Copy(out, in)
				_ = out.(*net.TCPConn).CloseWrite()
			}()
			_, _ = io.Copy(in, out)
		}()
	}
}

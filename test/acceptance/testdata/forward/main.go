// Command forward joins the run containers of a @docker scenario to the
// relay on a host whose Docker daemon runs in a virtual machine, as Docker
// Desktop's does: it listens on its first argument, a loopback address in
// the network namespace the run containers share, and copies each
// connection to and from its second, the relay as host.docker.internal
// names it.
package main

import (
	"io"
	"net"
	"os"
)

func main() {
	ln, err := net.Listen("tcp", os.Args[1])
	if err != nil {
		os.Exit(1)
	}
	for {
		in, err := ln.Accept()
		if err != nil {
			os.Exit(1)
		}
		go func() {
			defer in.Close()
			out, err := net.Dial("tcp", os.Args[2])
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

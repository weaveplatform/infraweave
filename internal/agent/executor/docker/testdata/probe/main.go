// Command probe stands in for the launcher's binary in the Docker
// executor's test. It reads its standard input, prints what the container
// gave it as JSON, and then exits with the code its input names, or, for
// the input "wait", waits for SIGINT and exits 130. Run as runner
// hand-back, it writes the JSON to the file handed-back in its working
// directory instead, and exits 0.
package main

import (
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	in, _ := io.ReadAll(os.Stdin)
	wd, _ := os.Getwd()
	got := map[string]any{
		"uid": os.Getuid(), "gid": os.Getgid(), "pid": os.Getpid(),
		"args": os.Args, "wd": wd, "stdin": string(in),
	}
	if len(os.Args) == 3 && os.Args[2] == "hand-back" {
		b, _ := json.Marshal(got)
		if err := os.WriteFile("handed-back", b, 0o644); err != nil {
			os.Exit(1)
		}
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(got)
	cmd := strings.TrimSpace(string(in))
	if cmd == "wait" {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT)
		<-sig
		_, _ = os.Stdout.WriteString("interrupted\n")
		os.Exit(130)
	}
	code, _ := strconv.Atoi(cmd)
	os.Exit(code)
}

package exec

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Shell is the shell a run's session runs in, from the image's PATH.
const Shell = "sh"

// waitDelay bounds how long a session's end waits for a process the
// shell left behind to close the output it inherited.
const waitDelay = 5 * time.Second

// ErrExited marks a session whose shell has exited, as a step's exit or
// a syntax error the shell cannot recover from ends it.
var ErrExited = errors.New("the session's shell exited")

// session is one shell the steps of a run share, so a variable one step
// exports is set in every later one. The shell reads the steps from its
// standard input and writes each step's exit status to descriptor 3,
// which no step's commands inherit.
type session struct {
	cmd    *osexec.Cmd
	sh     string
	steps  io.WriteCloser
	status *bufio.Reader
	file   *os.File
	dir    string
	out    io.Writer
}

// start starts a session in dir with env, its steps' output written to
// out. The shell is found on env's PATH, which is the one its steps see.
func start(ctx context.Context, dir string, env []string, out io.Writer) (*session, error) {
	sh, err := find(Shell, lookup(env, "PATH"))
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("start the shell: %w", err)
	}
	cmd := osexec.CommandContext(ctx, sh)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, out, out
	cmd.ExtraFiles = []*os.File{w}
	cmd.WaitDelay = waitDelay
	steps, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	_ = w.Close()
	if err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("start the shell: %w", err)
	}
	return &session{
		cmd: cmd, sh: sh, steps: steps, status: bufio.NewReader(r), file: r, dir: dir, out: out,
	}, nil
}

// find is the path of the executable file name in the first directory of
// path, a PATH value, that holds one.
func find(name, path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() &&
			info.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("start the shell: %s: %w", name, osexec.ErrNotFound)
}

// run runs text, one hook or command, as a group of its own, with no
// standard input, and answers its exit status. Text the shell cannot
// parse answers the status of the shell's check, 2, without reaching the
// session; a step that ends the shell answers the shell's exit status
// and ErrExited, after which the session runs nothing.
func (s *session) run(ctx context.Context, text string) (int, error) {
	group := "{ " + text + "\n}"
	if code, err := s.check(ctx, group); code != 0 || err != nil {
		return code, err
	}
	step := "if " + group + " </dev/null 3>&-; then printf '0\\n' >&3; " +
		"else printf '%d\\n' \"$?\" >&3; fi\n"
	if _, err := io.WriteString(s.steps, step); err != nil {
		return s.exited()
	}
	line, err := s.status.ReadString('\n')
	if err != nil {
		return s.exited()
	}
	code, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return 0, fmt.Errorf("the shell's status %q: %w", line, err)
	}
	return code, nil
}

// check parses group in a shell of its own, which executes nothing and
// writes what it cannot parse to the session's output. An unterminated
// quote or here-document would otherwise read the session's later steps
// as its own text.
func (s *session) check(ctx context.Context, group string) (int, error) {
	cmd := osexec.CommandContext(ctx, s.sh, "-n") //nolint:gosec // the session's own shell
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = s.dir, strings.NewReader(group), s.out, s.out
	err := cmd.Run()
	var exit *osexec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("check a step's syntax: %w", err)
	}
	return 0, nil
}

// exited answers the exit status of a shell whose steps or status pipe
// closed, once it has ended.
func (s *session) exited() (int, error) {
	err := s.close()
	var exit *osexec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), ErrExited
	}
	if err != nil {
		return 0, err
	}
	return 0, ErrExited
}

// close ends the session: the shell reads the end of its steps and exits.
// Closing it again answers nil.
func (s *session) close() error {
	if s.cmd == nil {
		return nil
	}
	_ = s.steps.Close()
	err := s.cmd.Wait()
	_ = s.file.Close()
	s.cmd = nil
	if err != nil {
		return fmt.Errorf("the shell: %w", err)
	}
	return nil
}

// quote is s as one word of the shell's language.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// line is args as one shell command, its standard output redirected to
// stdout unless that is empty.
func line(args []string, stdout string) string {
	var b bytes.Buffer
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(quote(a))
	}
	if stdout != "" {
		b.WriteString(" >" + quote(stdout))
	}
	return b.String()
}

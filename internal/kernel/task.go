package kernel

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxTaskCommand bounds a task's command, in characters: taskCreate's
// command and the run specification's task.command have it as their
// maxLength.
//
// ref: decision 0003 § "Bounds in the domains"
const MaxTaskCommand = 4096

// Task is the work of a TASK run: the command it runs in its stack's
// workspace, and whether it runs it without initializing the tool first.
//
// ref: decision 0008 § "The run specification and its secrets"
type Task struct {
	Command            string
	SkipInitialization bool
}

// Check refuses t's command at at when it is blank, holds a NUL byte,
// which no shell word carries, or is longer than MaxTaskCommand.
func (t Task) Check(at string) []FieldError {
	switch {
	case strings.TrimSpace(t.Command) == "":
		return []FieldError{
			{Pointer: at, Reason: ReasonRequired, Message: "A task runs a command."},
		}
	case strings.ContainsRune(t.Command, 0):
		return []FieldError{{
			Pointer: at, Reason: ReasonRule, Message: "A command cannot hold a NUL byte.",
		}}
	case utf8.RuneCountInString(t.Command) > MaxTaskCommand:
		return []FieldError{{
			Pointer: at, Reason: ReasonMaxLength,
			Message: fmt.Sprintf("A command is at most %d characters.", MaxTaskCommand),
		}}
	}
	return nil
}

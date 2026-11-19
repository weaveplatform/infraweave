package vcslab

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	git "github.com/go-git/go-git/v5"
)

// Azure requires multi_ack on a full clone, which go-git does not implement.
// The lab already requires Git to seed its repositories. Use it for this
// disposable clone without changing go-git's process-wide capabilities (which
// would also affect concurrent production source verification).
func cloneAzureWithGit(ctx context.Context, dir, remote string, credential GitCredentials) (*git.Repository, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, ErrNative
	}
	const helper = `!f() { printf 'username=%s\npassword=%s\n' "$INFRAWEAVE_LAB_GIT_USER" "$INFRAWEAVE_LAB_GIT_TOKEN"; }; f`
	command := exec.CommandContext(ctx, "git", "-c", "credential.helper=", "-c", "credential.helper="+helper,
		"clone", "--quiet", "--branch", "main", "--", remote, dir)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") && !strings.HasPrefix(entry, "INFRAWEAVE_LAB_GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"INFRAWEAVE_LAB_GIT_USER="+credential.User, "INFRAWEAVE_LAB_GIT_TOKEN="+credential.Token)
	if credential.CAFile != "" {
		command.Env = append(command.Env, "GIT_SSL_CAINFO="+credential.CAFile)
	}
	// Git's output can contain the remote URL, credentials and provider text.
	// Discard it rather than retaining it in an error or a test failure.
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: native Git clone failed", ErrNative)
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, ErrNative
	}
	return repo, nil
}

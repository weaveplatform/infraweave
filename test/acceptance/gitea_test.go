package acceptance

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/cucumber/godog"
)

// giteaSteps seed and read the scenario's repositories on the @vcs Gitea.
// A scenario names its repositories, and each name maps to a repository of
// the scenario's own, so scenarios running in parallel never share one.
//
// ref: decision 0016
type giteaSteps struct {
	w     *world
	gitea *Gitea
	// repos maps a scenario's repository name to the repository Gitea holds.
	repos map[string]string
	// last is each repository's newest commit the scenario made.
	last map[string]string
	// kept are commit SHAs the scenario kept by name.
	kept map[string]string
}

func (g *giteaSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a Gitea repository "([^"]+)" with the files:$`, g.repository)
	sc.Step(`^a commit to "([^"]+)" of "([^"]+)" changing "([^"]+)"$`, g.commit)
	sc.Step(`^the head of "([^"]+)" of "([^"]+)" is kept as "([^"]+)"$`, g.keepHead)
	sc.Step(`^the head of "([^"]+)" of "([^"]+)" is its last commit$`, g.headIsLast)
	sc.Step(`^the commits kept as "([^"]+)" and "([^"]+)" differ$`, g.differ)
	sc.Step(`^the commits kept as "([^"]+)" and "([^"]+)" are the same$`, g.same)
}

// ErrGiteaStep reports a repository or commit a step names that the
// scenario has not made, or a head that is not the one expected.
var ErrGiteaStep = errors.New("gitea step")

func (g *giteaSteps) start(ctx context.Context) error {
	if g.gitea != nil {
		return nil
	}
	gt, err := g.w.s.deps.Gitea(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names @vcs
	}
	g.gitea = gt
	g.repos, g.last, g.kept = map[string]string{}, map[string]string{}, map[string]string{}
	return nil
}

func (g *giteaSteps) repository(ctx context.Context, name string, table *godog.Table) error {
	if err := g.start(ctx); err != nil {
		return err
	}
	files := map[string]string{}
	for i, row := range table.Rows {
		if len(row.Cells) != 2 {
			return fmt.Errorf("%w: row %d: want path and content", ErrGiteaStep, i+1)
		}
		files[row.Cells[0].Value] = row.Cells[1].Value
	}
	repo := name + "-" + strconv.FormatInt(g.w.s.deps.repos.Add(1), 10)
	sha, err := g.gitea.CreateRepository(ctx, repo, files)
	if err != nil {
		return err
	}
	g.repos[name], g.last[name] = repo, sha
	return nil
}

func (g *giteaSteps) repo(name string) (string, error) {
	repo, ok := g.repos[name]
	if !ok {
		return "", fmt.Errorf("%w: no repository %q in this scenario", ErrGiteaStep, name)
	}
	return repo, nil
}

// commit changes path on branch to content no earlier commit wrote, so
// every commit has a tree of its own.
func (g *giteaSteps) commit(ctx context.Context, branch, name, path string) error {
	repo, err := g.repo(name)
	if err != nil {
		return err
	}
	content := "change " + strconv.FormatInt(g.w.s.deps.repos.Add(1), 10) + "\n"
	sha, err := g.gitea.Commit(ctx, repo, branch, map[string]string{path: content}, "change "+path)
	if err != nil {
		return err
	}
	g.last[name] = sha
	return nil
}

func (g *giteaSteps) head(ctx context.Context, branch, name string) (string, error) {
	repo, err := g.repo(name)
	if err != nil {
		return "", err
	}
	return g.gitea.Head(ctx, repo, branch)
}

func (g *giteaSteps) keepHead(ctx context.Context, branch, name, key string) error {
	sha, err := g.head(ctx, branch, name)
	if err != nil {
		return err
	}
	g.kept[key] = sha
	return nil
}

func (g *giteaSteps) headIsLast(ctx context.Context, branch, name string) error {
	sha, err := g.head(ctx, branch, name)
	if err != nil {
		return err
	}
	if sha != g.last[name] {
		return fmt.Errorf(
			"%w: head of %s of %s is %s, want %s",
			ErrGiteaStep,
			branch,
			name,
			sha,
			g.last[name],
		)
	}
	return nil
}

func (g *giteaSteps) differ(a, b string) error {
	if g.kept[a] == "" || g.kept[b] == "" {
		return fmt.Errorf("%w: %q and %q are not both kept", ErrGiteaStep, a, b)
	}
	if g.kept[a] == g.kept[b] {
		return fmt.Errorf("%w: %q and %q are both %s", ErrGiteaStep, a, b, g.kept[a])
	}
	return nil
}

func (g *giteaSteps) same(a, b string) error {
	if g.kept[a] == "" || g.kept[a] != g.kept[b] {
		return fmt.Errorf("%w: %q is %s and %q is %s", ErrGiteaStep, a, g.kept[a], b, g.kept[b])
	}
	return nil
}

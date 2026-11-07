package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

func (s *servedSteps) registerFeatures(sc *godog.ScenarioContext) {
	sc.Step(
		`^a running infraweave server whose features\.disabled is "([^"]+)"$`,
		s.runningSwitchedOff,
	)
	sc.Step(`^the problem names the feature "([a-z-]+)"$`, s.namesFeature)
	sc.Step(`^"([A-Z]+) ([^"]+)" on the API listener answers (\d+)$`, s.apiAnswers)
}

func (s *servedSteps) apiAnswers(ctx context.Context, method, path, code string) error {
	resp, err := s.send(ctx, method, "API", path)
	if err != nil {
		return err
	}
	if strconv.Itoa(resp.status) != code {
		return fmt.Errorf(
			"%w: %s %s answered %d, want %s: %s",
			errStep,
			method,
			path,
			resp.status,
			code,
			resp.body,
		)
	}
	return nil
}

// runningSwitchedOff starts the scenario's server with the comma-separated
// features switched off for the installation.
func (s *servedSteps) runningSwitchedOff(ctx context.Context, names string) error {
	off := strings.Split(names, ",")
	for i := range off {
		off[i] = strings.TrimSpace(off[i])
	}
	s.w.features = map[string]any{"disabled": off}
	_, err := s.w.startServer(ctx, nil)
	return err
}

// namesFeature checks the last answer's feature member, which the SDK's
// Problem does not carry.
func (s *servedSteps) namesFeature(feature string) error {
	var p struct {
		Feature string `json:"feature"`
	}
	if err := json.Unmarshal(s.last().body, &p); err != nil {
		return fmt.Errorf("%w: the answer is not a problem: %w: %s", errStep, err, s.last().body)
	}
	if p.Feature != feature {
		return fmt.Errorf("%w: the problem names feature %q, want %q", errStep, p.Feature, feature)
	}
	return nil
}

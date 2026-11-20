package gitlab

import (
	"errors"
	"testing"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitPushPagesContinuesFullPageWithoutNextHeader(t *testing.T) {
	calls := 0
	got, err := pushPages("test range", func(page int64) ([]int, *gitlab.Response, error) {
		calls++
		if page == 1 {
			return make([]int, 100), &gitlab.Response{}, nil
		}
		return []int{101}, &gitlab.Response{}, nil
	})
	if err != nil || len(got) != 101 || calls != 2 {
		t.Fatal("full native page was silently truncated", len(got), calls, err)
	}
}

func TestUnitPushPagesRefusesIncompletePagination(t *testing.T) {
	cause := errors.New("native failure")
	for _, mode := range []string{"missing values", "missing response", "backward page", "empty next page", "backend error"} {
		t.Run(mode, func(t *testing.T) {
			_, err := pushPages("test range", func(int64) ([]int, *gitlab.Response, error) {
				items, response := []int{}, &gitlab.Response{}
				switch mode {
				case "missing values":
					items = nil
				case "missing response":
					response = nil
				case "backward page":
					items, response.NextPage = []int{1}, 1
				case "empty next page":
					response.NextPage = 2
				case "backend error":
					return nil, nil, cause
				}
				return items, response, nil
			})
			if !errors.Is(err, kernel.ErrUnavailable) || mode == "backend error" && !errors.Is(err, cause) {
				t.Fatal("incomplete native pagination became complete", err)
			}
		})
	}
}

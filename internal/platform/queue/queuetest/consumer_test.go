package queuetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

var errConsumerContract = errors.New("consumer contract failure")

type consumerQueue struct{ stage string }

func (q consumerQueue) Enqueue(context.Context, queue.Kind, any, ...queue.Option) (int64, error) {
	if q.stage == "enqueue" {
		return 0, errConsumerContract
	}
	return 1, nil
}

func (q consumerQueue) Claim(context.Context, queue.Name, []string, int, time.Duration, string) ([]queue.Job, error) {
	if q.stage == "claim" {
		return nil, errConsumerContract
	}
	if q.stage == "empty" {
		return nil, nil
	}
	return []queue.Job{{ID: 1}}, nil
}

func (q consumerQueue) Complete(context.Context, queue.Job) error {
	if q.stage == "uncompleted" {
		return nil
	}
	if q.stage == "completion error" {
		return errConsumerContract
	}
	return queue.ErrLeaseLost
}

func TestUnitConsumeTwiceContract(t *testing.T) {
	for _, stage := range []string{"", "enqueue", "claim", "empty", "consume", "uncompleted", "completion error"} {
		t.Run(stage, func(t *testing.T) {
			calls := 0
			err := ConsumeTwice(t.Context(), consumerQueue{stage: stage}, work, kernel.NewULID(), func(context.Context, queue.Job) error {
				calls++
				if stage == "consume" {
					return errConsumerContract
				}
				return nil
			})
			if (err != nil) != (stage != "") {
				t.Fatalf("contract = %v", err)
			}
			if stage == "" && calls != 2 {
				t.Fatalf("consumed %d times", calls)
			}
		})
	}
}

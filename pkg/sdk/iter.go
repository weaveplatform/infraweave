package sdk

import (
	"context"
	"iter"
)

// Page fetches the page of a list operation that cursor names, nil for the
// first, and returns its items and the nextCursor of the answer's
// {items, nextCursor} envelope (decision 0003).
type Page[T any] func(ctx context.Context, cursor *string) (items []T, next *string, err error)

// Iterate yields every item of a list operation, fetching each page as the
// last one runs out, until a page's nextCursor is absent or empty. An error
// is yielded once, and ends the iteration.
//
//	for stack, err := range sdk.Iterate(ctx, func(ctx context.Context, c *string) ([]api.Stack, *string, error) {
//		r, err := client.API.StacksWithResponse(ctx, &api.StacksParams{Cursor: c})
//		if err == nil {
//			err = sdk.Check(r.HTTPResponse, r.Body)
//		}
//		if err != nil {
//			return nil, nil, err
//		}
//		return r.JSON200.Items, r.JSON200.NextCursor, nil
//	}) { … }
func Iterate[T any](ctx context.Context, page Page[T]) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var cursor *string
		for {
			items, next, err := page(ctx, cursor)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, it := range items {
				if !yield(it, nil) {
					return
				}
			}
			if next == nil || *next == "" {
				return
			}
			cursor = next
		}
	}
}

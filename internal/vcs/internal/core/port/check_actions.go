package port

import (
	"context"
	"errors"
	"time"
)

// ErrCheckActionTarget refuses native facts belonging to another repository.
var ErrCheckActionTarget = errors.New("vcs: check action source changed")

// ActionFacts contains immutable commit metadata and its source binding.
// NativeHost includes the App identity;
// a changed host or App cannot inherit an earlier authorization decision.
type ActionFacts struct {
	Commit     ActionCommit `json:"commit"`
	NativeHost string       `json:"nativeHost"`
}

// CheckActions reads repository write access through the installed GitHub App.
// It performs no mutation and applies only to GitHub check-button deliveries.
type CheckActions interface {
	CheckHost
	CanWrite(context.Context, Repository, string) (bool, error)
}

// ActionCommit is the owned JSON representation of immutable native metadata.
type ActionCommit struct {
	Hash        string    `json:"hash"`
	Tag         string    `json:"tag,omitempty"`
	URL         string    `json:"url,omitempty"`
	Message     string    `json:"message"`
	AuthorName  string    `json:"authorName"`
	AuthorLogin *string   `json:"authorLogin,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

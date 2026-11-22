package sampling

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// ErrNotFound is an account-qualified missing input or evaluation.
var ErrNotFound = errors.New("policy input not found")

// Retention starts at evaluation, not delayed upload completion.
const Retention = 7 * 24 * time.Hour

// UploadKind keeps input, source, decisions and print out of queue payloads.
var UploadKind = queue.Kind{Name: "policies.input-upload", Queue: queue.Default, Policy: queue.Ignore}

// Evaluation carries private evidence only inside the policy domain.
type Evaluation struct {
	Receipt        policyapi.Receipt
	Body           string
	Input          json.RawMessage
	Print          string
	PrintTruncated bool
}

// Payload is the sealed sample document. It preserves the input OPA evaluated.
type Payload struct {
	Input          json.RawMessage    `json:"input"`
	Body           string             `json:"body"`
	Engine         string             `json:"engineType"`
	Result         json.RawMessage    `json:"result"`
	Outcome        policyapi.Outcome  `json:"outcome"`
	Error          *policyapi.Failure `json:"error,omitempty"`
	Print          string             `json:"print"`
	PrintTruncated bool               `json:"printTruncated"`
}

// Input is one immutable upload's metadata and row-bound encrypted staging.
type Input struct {
	Account              kernel.AccountID
	ID                   kernel.ULID
	Key, State           string
	CreatedAt, ExpiresAt time.Time
	PublishedAt          *time.Time
	Staging              crypt.Value
	SpaceSnapshot        *kernel.ULID
	CaptureKind          string
}

// Store owns evaluation metadata and the atomic account/hour quota.
type Store interface {
	CountableInputs(context.Context, kernel.AccountID, []kernel.ULID, []kernel.ULID, *kernel.ULID, time.Time, int) ([]CountableInput, error)
	AttachUploadJob(context.Context, kernel.AccountID, kernel.ULID, int64) error
	InspectInput(context.Context, kernel.AccountID, kernel.ULID, bool) (Input, error)
	DueInputs(context.Context, time.Time, int) ([]Input, error)
	RetirementDue(context.Context, Input, time.Time) (bool, error)
	PolicyInputs(context.Context, kernel.AccountID, kernel.ULID, int) ([]Input, error)
	AccountInputs(context.Context, kernel.AccountID, int) ([]Input, error)
	DropInput(context.Context, Input) error
	PurgeMetadata(context.Context, kernel.AccountID, int) (bool, error)
	CaptureSlot(context.Context, kernel.AccountID, kernel.ULID, string) (CaptureSlot, bool, error)
	SetCapturePending(context.Context, Input) error
	PublishCapture(context.Context, Input) (*kernel.ULID, bool, error)
	ClearCapturePending(context.Context, Input) error
	References(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]Reference, error)
	Reserve(context.Context, kernel.AccountID, policyapi.Type) (bool, error)
	CreateInput(context.Context, Input, []byte) error
	RecordEvaluation(context.Context, policyapi.EvaluationRequest, kernel.ULID, policyapi.Receipt, *Input, inputs.EvaluationFacts) error
	Input(context.Context, kernel.AccountID, kernel.ULID, bool) (Input, error)
	StartUpload(context.Context, kernel.AccountID, kernel.ULID) error
	FinishInput(context.Context, kernel.AccountID, kernel.ULID, string, time.Time) error
}

// Objects is the blob owner's sealed immutable-write and transactional lifecycle.
type Objects interface {
	AvailableBatch(context.Context, objectstore.Name, []string, time.Time) (map[string]bool, error)
	Available(context.Context, objectstore.Name, string, time.Time) (bool, error)
	Put(context.Context, objectstore.Name, string, io.Reader, int64) (objectstore.Object, error)
	CommitUntil(context.Context, objectstore.Name, string, time.Time) error
	Uploaded(context.Context, objectstore.Name, string) (bool, error)
	Delete(context.Context, objectstore.Name, string) error
	LockLiveAt(context.Context, objectstore.Name, string, time.Time) (objectstore.Object, error)
	Open(context.Context, objectstore.Name, string) (io.ReadCloser, error)
}

// Deps use the existing primary connection budget and local keyring.
type Deps struct {
	DB      tx.Beginner
	Store   Store
	Queue   *queue.Queue
	Objects Objects
	Now     func() time.Time
}

// Service stages evaluations and publishes sealed samples independently.
type Service struct{ d Deps }

// New constructs retention without starting a worker or allocating a new pool.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// CaptureSlot is held by the transaction that reserved this minute's refresh.
type CaptureSlot struct {
	At               time.Time
	Current, Pending *kernel.ULID
}

// CountableInput is an owned live sample candidate, without source or input.
type CountableInput struct {
	ID, Policy kernel.ULID
	Key        string
}

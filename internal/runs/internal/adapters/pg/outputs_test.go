package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestAccOutputs checks that a run's outputs replace its stack's, each
// value sealed, and read back by name with the value of each that is not
// sensitive; that a later run's outputs replace them all, none clearing
// them; that a value copied to another stack or name does not open; and
// that another account reads none.
//
// ref: decision 0013 § "Sealed columns"
func TestAccOutputs(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db, keyring(t, db))
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	first, err := s.Handed(ctx, insertRun(t, db, acct, stack, false))
	if err != nil {
		t.Fatal(err)
	}
	read := func() []stacksapi.Output {
		t.Helper()
		got, err := s.Outputs(ctx, acct, []kernel.ULID{stack})
		if err != nil {
			t.Fatal(err)
		}
		return got[stack]
	}
	if got := read(); len(got) != 0 {
		t.Errorf("the outputs of a stack never applied = %+v", got)
	}

	if err := s.ReplaceOutputs(ctx, first, map[string]plan.Output{
		"name":     {Value: json.RawMessage(`"web"`), Description: "The server's name"},
		"password": {Value: json.RawMessage(`"hunter2"`), Sensitive: true},
		"ports":    {Value: json.RawMessage(`[80, 443]`)},
	}); err != nil {
		t.Fatal(err)
	}
	got := read()
	if len(got) != 3 ||
		got[0].Name != "name" || string(got[0].Value) != `"web"` ||
		got[0].Description != "The server's name" || got[0].Sensitive ||
		got[1].Name != "password" || got[1].Value != nil || !got[1].Sensitive ||
		got[2].Name != "ports" || string(got[2].Value) != `[80, 443]` ||
		got[2].Description != "" {
		t.Errorf("the stack's outputs = %+v", got)
	}
	rows, err := db.Query(ctx, `SELECT value_sealed FROM stack_outputs WHERE account_id = $1`,
		acct)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var sealed []byte
		if err := rows.Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		for _, plain := range []string{"web", "hunter2", "443"} {
			if bytes.Contains(sealed, []byte(plain)) {
				t.Errorf("a sealed value holds %q", plain)
			}
		}
	}
	rows.Close()

	elsewhere := kernel.NewULID()
	if _, err := db.Exec(ctx, `INSERT INTO stack_outputs (account_id, stack_id, name, run_id,
		value_sealed) SELECT account_id, $2, name, run_id, value_sealed FROM stack_outputs
		WHERE account_id = $1 AND name = 'name'`, acct, elsewhere); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Outputs(ctx, acct, []kernel.ULID{elsewhere}); err == nil {
		t.Error("a value copied to another stack opened")
	}
	if _, err := db.Exec(ctx, `INSERT INTO stack_outputs (account_id, stack_id, name, run_id,
		value_sealed) SELECT account_id, stack_id, 'copy', run_id, value_sealed FROM stack_outputs
		WHERE account_id = $1 AND stack_id = $2 AND name = 'name'`, acct, stack); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Outputs(ctx, acct, []kernel.ULID{stack}); err == nil {
		t.Error("a value copied to another name opened")
	}

	second, err := s.Handed(ctx, insertRun(t, db, acct, stack, false))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceOutputs(ctx, second, map[string]plan.Output{
		"name": {Value: json.RawMessage(`"api"`)},
	}); err != nil {
		t.Fatal(err)
	}
	var writer kernel.ULID
	if err := db.QueryRow(ctx, `SELECT run_id FROM stack_outputs WHERE account_id = $1`,
		acct).Scan(&writer); err != nil || writer != second.ID {
		t.Errorf("the outputs' run = %s, %v, want %s", writer, err, second.ID)
	}
	if got := read(); len(got) != 1 || got[0].Name != "name" || string(got[0].Value) != `"api"` ||
		got[0].Description != "" {
		t.Errorf("the outputs a later apply replaced = %+v", got)
	}
	if got, err := s.Outputs(ctx, kernel.NewAccountID(), []kernel.ULID{stack}); err != nil ||
		len(got) != 0 {
		t.Errorf("another account's outputs = %+v, %v", got, err)
	}
	if err := s.ReplaceOutputs(ctx, second, map[string]plan.Output{}); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got) != 0 {
		t.Errorf("the outputs of an apply with none = %+v", got)
	}
}

// TestAccOutputsUnsealed checks that outputs the keyring cannot seal are
// not written, and that a stored value it cannot open is not read.
func TestAccOutputsUnsealed(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	run, err := pg.New(db, nil).Handed(ctx, insertRun(t, db, acct, stack, true))
	if err != nil {
		t.Fatal(err)
	}
	broken := pg.New(db, failingSealer{})
	if err := broken.ReplaceOutputs(ctx, run, map[string]plan.Output{
		"name": {Value: json.RawMessage(`"web"`)},
	}); !errors.Is(err, errSeal) {
		t.Errorf("ReplaceOutputs with a failing keyring = %v", err)
	}
	if _, err := broken.Outputs(ctx, acct, []kernel.ULID{stack}); !errors.Is(err, errSeal) {
		t.Errorf("Outputs with a failing keyring = %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE stack_outputs SET sensitive = true
		WHERE account_id = $1`, acct); err != nil {
		t.Fatal(err)
	}
	if got, err := broken.Outputs(ctx, acct, []kernel.ULID{stack}); err != nil ||
		len(got[stack]) != 1 || !got[stack][0].Sensitive {
		t.Errorf("a sensitive output, never opened = %+v, %v", got, err)
	}
}

// TestAccOutputsUnavailable fails each outputs method on a closed pool.
func TestAccOutputsUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db, keyring(t, db))
	db.Close()
	ctx := context.Background()
	acct := kernel.NewAccountID()
	if _, err := s.Outputs(ctx, acct, []kernel.ULID{kernel.NewULID()}); err == nil {
		t.Error("Outputs on a closed pool succeeded")
	}
	run := lifecycle.Run{Account: acct, ID: kernel.NewULID(), Stack: kernel.NewULID()}
	if err := s.ReplaceOutputs(ctx, run, nil); err == nil {
		t.Error("ReplaceOutputs on a closed pool succeeded")
	}
}

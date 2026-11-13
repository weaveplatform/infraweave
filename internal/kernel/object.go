package kernel

import "fmt"

// RunObjectKey is the key of the object named name that acct's run run
// stores in state at state version version: acct/run/STATE-version/name.
// The server records and signs the object under it, and the runner
// derives the object's content key from it before the server answers it,
// so both build it here and cannot disagree on an object's key.
//
// ref: decision 0005 § "Keys"
// ref: decision 0013 § "Sealed objects"
func RunObjectKey(acct AccountID, run ULID, state RunState, version int64, name string) string {
	return fmt.Sprintf("%s/%s/%s-%d/%s", acct, run, state, version, name)
}

package objectstoretest

import (
	"os"
	"testing"
)

// IdentityEnv names the identity the objectstore-cloud job's credentials
// hold for a run: writer or deleter.
const IdentityEnv = "INFRAWEAVE_TEST_CLOUD_IDENTITY"

// Cloud returns the identity IdentityEnv names and the values of vars, in
// order, for a driver's test against its real service. It skips the test
// unless every one is set, as only the objectstore-cloud job sets them,
// and fails it on an identity other than writer or deleter.
func Cloud(t *testing.T, vars ...string) (Identity, []string) {
	t.Helper()
	values := make([]string, len(vars))
	for i, v := range append([]string{IdentityEnv}, vars...) {
		value := os.Getenv(v)
		if value == "" {
			t.Skipf("%s is not set: the objectstore-cloud job runs this test", v)
		}
		if i > 0 {
			values[i-1] = value
		}
	}
	switch id := os.Getenv(IdentityEnv); id {
	case "writer":
		return Writer, values
	case "deleter":
		return Deleter, values
	default:
		t.Fatalf("%s is %q, want writer or deleter", IdentityEnv, id)
		return Full, nil
	}
}

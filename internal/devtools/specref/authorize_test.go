package specref

import "testing"

// TestUnitScaffoldAuthorize checks the action and subject a scaffolded
// operation gets from the catalogue, and that an operation the catalogue
// cannot decide gets none, or no subject, for its author to write.
func TestUnitScaffoldAuthorize(t *testing.T) {
	rows := parseActionRows(t, actionRows+`
STACK_CREATE: {subject: stack, fallback: admin}
STACK_LOCK: {subject: stack, fallback: writer}
`)
	for _, tc := range []struct {
		method, path, id string
		action, subject  string
	}{
		{"post", "/stacks/{stackId}/lock", "stackLock", "STACK_LOCK", "path:stackId:stack"},
		{"post", "/stacks", "stackCreate", "STACK_CREATE", ""},
		{"get", "/stacks/{stackId}", "stack", "SPACE_READ", "path:stackId:stack"},
		{"get", "/stacks", "stacks", "SPACE_READ", "list"},
		{"get", "/things/{thingId}", "thing", "SPACE_READ", ""},
		{"get", "/api-keys", "apiKeys", "API_KEY_READ", "any"},
		{"get", "/api-keys/{apiKeyId}", "apiKey", "API_KEY_READ", "any"},
		{"delete", "/api-keys/{apiKeyId}", "apiKeyDelete", "API_KEY_DELETE", "account"},
		{"post", "/stacks/{stackId}/fly", "stackFly", "", ""},
	} {
		op := ymap{"operationId": tc.id}
		authorize(tc.method, tc.path, op, rows)
		action, _ := op["x-infraweave-action"].(string)
		subject, _ := op["x-infraweave-subject"].(string)
		if action != tc.action || subject != tc.subject {
			t.Errorf("%s %s: %q %q, want %q %q", tc.method, tc.path, action, subject,
				tc.action, tc.subject)
		}
	}
}

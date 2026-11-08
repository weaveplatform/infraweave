package specref

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// actionSchema is an introspection whose Action enum has a plain value, a
// disabled one and a deprecated one.
const actionSchema = `{"data": {"__schema": {"types": [{"kind": "ENUM", "name": "Action",
	"enumValues": [
		{"name": "SPACE_READ", "description": ""},
		{"name": "STACK_WEBHOOK_CREATE", "description": "Disabled. Do not use."},
		{"name": "UPDATE_BLUEPRINT_DEPLOYMENT", "description": "",
			"isDeprecated": true, "deprecationReason": "Use UPGRADE_BLUEPRINT_DEPLOYMENT instead."}
	]}]}}}`

const actionRows = `
SPACE_READ: {subject: space, fallback: reader}
STACK_WEBHOOK_CREATE: {subject: stack, fallback: admin, disabled: true}
UPDATE_BLUEPRINT_DEPLOYMENT: {subject: template_deployment, fallback: admin, deprecated: true}
`

func parseActionSchema(t *testing.T, data string) *Schema {
	t.Helper()
	s, err := ParseIntrospection([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func parseActionRows(t *testing.T, data string) map[string]ActionRow {
	t.Helper()
	rows, err := ParseActionCatalogue([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestUnitActionsGolden(t *testing.T) {
	got, err := GenerateActions(parseActionSchema(t, actionSchema), parseActionRows(t, actionRows))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "actions.gen.go", got)
	for _, want := range []string{
		`Action = "SPACE_READ"`,
		`{Action: ActionStackWebhookCreate, Subject: "stack", Fallback: FallbackAdmin, Disabled: true, Deprecated: false}`,
		`{Action: ActionUpdateBlueprintDeployment, Subject: "template_deployment", Fallback: FallbackAdmin, Disabled: false, Deprecated: true}`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("actions.gen.go lacks %q", want)
		}
	}
}

// TestUnitActionsMismatch checks that each disagreement between the
// catalogue and the enum fails, naming the value.
func TestUnitActionsMismatch(t *testing.T) {
	s := parseActionSchema(t, actionSchema)
	for name, tc := range map[string]struct{ rows, want string }{
		"a value with no row": {
			strings.Replace(actionRows, "SPACE_READ: {subject: space, fallback: reader}\n", "", 1),
			"SPACE_READ has no row",
		},
		"a stray row": {
			actionRows + "SPACE_FLY: {subject: space, fallback: admin}\n",
			"SPACE_FLY is not in the enum",
		},
		"no subject": {
			strings.Replace(actionRows, "subject: space, ", "", 1), "SPACE_READ has no subject",
		},
		"a fallback no level names": {
			strings.Replace(actionRows, "fallback: reader", "fallback: owner", 1),
			`SPACE_READ's fallback "owner" is not reader, writer or admin`,
		},
		"a disabled flag the schema lacks": {
			strings.Replace(actionRows, "fallback: reader", "fallback: reader, disabled: true", 1),
			"SPACE_READ is disabled: false in the schema",
		},
		"a deprecated flag missing": {
			strings.Replace(actionRows, ", deprecated: true", "", 1),
			"UPDATE_BLUEPRINT_DEPLOYMENT is deprecated: true in the schema",
		},
	} {
		_, err := GenerateActions(s, parseActionRows(t, tc.rows))
		if !errors.Is(err, ErrActions) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want ErrActions naming %q", name, err, tc.want)
		}
	}
	none := parseActionSchema(t, `{"data": {"__schema": {"types": []}}}`)
	if _, err := GenerateActions(none, nil); !errors.Is(err, ErrActions) {
		t.Errorf("a schema with no Action enum = %v", err)
	}
	if _, err := ParseActionCatalogue(
		[]byte("SPACE_READ: {subject: space, level: admin}"),
	); err == nil {
		t.Error("a row with an unknown key parsed")
	}
}

// TestUnitActionsFile writes the catalogue from a reference directory,
// checks it, and fails the check once the file or a row changes.
func TestUnitActionsFile(t *testing.T) {
	dir := t.TempDir()
	cat, out := filepath.Join(dir, "actions.yaml"), filepath.Join(dir, "actions.gen.go")
	for path, data := range map[string]string{
		filepath.Join(dir, FileIntrospection): actionSchema,
		cat:                                   actionRows,
	} {
		if err := os.WriteFile(path, []byte(data), filePerm); err != nil {
			t.Fatal(err)
		}
	}
	if err := ActionsFile(dir, cat, out, true); !errors.Is(err, ErrDrift) {
		t.Errorf("check before writing = %v, want ErrDrift", err)
	}
	if err := ActionsFile(dir, cat, out, false); err != nil {
		t.Fatal(err)
	}
	if err := ActionsFile(dir, cat, out, true); err != nil {
		t.Errorf("check right after write: %v", err)
	}
	if err := os.WriteFile(cat, []byte(actionRows+"SPACE_FLY: {subject: space, fallback: admin}\n"),
		filePerm); err != nil {
		t.Fatal(err)
	}
	if err := ActionsFile(dir, cat, out, true); !errors.Is(err, ErrActions) {
		t.Errorf("check with a stray row = %v, want ErrActions", err)
	}
	if err := os.WriteFile(cat, []byte(actionRows), filePerm); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][3]string{
		"no introspection":   {filepath.Join(dir, "none"), cat, out},
		"no catalogue":       {dir, filepath.Join(dir, "none.yaml"), out},
		"an unwritable file": {dir, cat, filepath.Join(dir, "none", "actions.gen.go")},
	} {
		if err := ActionsFile(args[0], args[1], args[2], false); err == nil {
			t.Errorf("%s: wrote", name)
		}
	}
	if err := os.WriteFile(cat, []byte("- a list"), filePerm); err != nil {
		t.Fatal(err)
	}
	if err := ActionsFile(dir, cat, out, false); err == nil {
		t.Error("a catalogue that is not a mapping wrote")
	}
	if err := os.WriteFile(
		filepath.Join(dir, FileIntrospection),
		[]byte("{}"),
		filePerm,
	); err != nil {
		t.Fatal(err)
	}
	if err := ActionsFile(dir, cat, out, false); err == nil {
		t.Error("an introspection with no schema wrote")
	}
}

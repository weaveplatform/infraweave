package specref

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The Flows fixtures are small synthetic clients written in the upstream
// styles: a provider package calling CallFlowsAPI with literal and constant
// paths, a flowctl api.ts calling apiFetch, and an SDK declaration file.
// Together they reach every verb rule and every shape the readers convert.

const flowsProviderClient = `package provider

func CallFlowsAPI[ReqT any, ResT any](c Config, urlPath string, req ReqT) (*ResT, error) {
	return nil, nil
}
`

const flowsProviderFlows = `package provider

import (
	"encoding/json"
	"time"
)

const getWidgetPath = "/provider/widgets/get"

type CreateWidgetRequest struct {
	ProjectID string            ` + "`json:\"projectId\"`" + `
	Name      string            ` + "`json:\"name\"`" + `
	Limit     *int64            ` + "`json:\"limit,omitempty\"`" + `
	Labels    map[string]string ` + "`json:\"labels,omitempty\"`" + `
	Style     *Style            ` + "`json:\"style\"`" + `
	Tags      []string          ` + "`json:\"tags\"`" + `
	internal  string
	Skipped   string ` + "`json:\"-\"`" + `
	Untagged  bool
}

type Style struct {
	Color string ` + "`json:\"color\"`" + `
}

type CreateWidgetResponse struct {
	Widget struct {
		ID string ` + "`json:\"id\"`" + `
	} ` + "`json:\"widget\"`" + `
}

type GetWidgetRequest struct {
	WidgetID string ` + "`json:\"widgetId\"`" + `
}

type GetWidgetResponse struct {
	Name      string          ` + "`json:\"name\"`" + `
	Ratio     float64         ` + "`json:\"ratio\"`" + `
	Count     int             ` + "`json:\"count\"`" + `
	Created   time.Time       ` + "`json:\"created\"`" + `
	Raw       json.RawMessage ` + "`json:\"raw\"`" + `
	Extra     any             ` + "`json:\"extra\"`" + `
	Meta      interface{}     ` + "`json:\"meta\"`" + `
	Reset     **int64         ` + "`json:\"reset\"`" + `
	Parts     []Part          ` + "`json:\"parts\"`" + `
}

type Part struct {
	Key string ` + "`json:\"key\"`" + `
}

type UpdateWidgetRequest struct {
	ID   string  ` + "`json:\"id\"`" + `
	Name *string ` + "`json:\"name,omitempty\"`" + `
}

type DeleteWidgetRequest struct {
	ID string ` + "`json:\"id\"`" + `
}

type AttachRequest struct {
	WidgetID string ` + "`json:\"widgetId\"`" + `
	FlowID   string ` + "`json:\"flowId\"`" + `
}

type ExportRequest struct {
	FlowID string ` + "`json:\"flowId\"`" + `
}

type ExportResponse struct {
	Definition string ` + "`json:\"definition\"`" + `
}

type SecretRequest struct {
	ProjectID string ` + "`json:\"projectId\"`" + `
	Key       string ` + "`json:\"key\"`" + `
}

type ListProjectsRequest struct{}

type ListProjectsResponse struct {
	Projects []Project ` + "`json:\"projects\"`" + `
}

type Project struct {
	ID   string ` + "`json:\"id\"`" + `
	Name string ` + "`json:\"name\"`" + `
}

type FindRequest struct {
	Name string ` + "`json:\"name\"`" + `
}

func calls(c Config) {
	CallFlowsAPI[CreateWidgetRequest, CreateWidgetResponse](c, "/provider/widgets/create", CreateWidgetRequest{})
	CallFlowsAPI[GetWidgetRequest, GetWidgetResponse](c, getWidgetPath, GetWidgetRequest{})
	CallFlowsAPI[GetWidgetRequest, GetWidgetResponse](c, getWidgetPath, GetWidgetRequest{})
	CallFlowsAPI[UpdateWidgetRequest, struct{}](c, "/provider/widgets/update", UpdateWidgetRequest{})
	CallFlowsAPI[DeleteWidgetRequest, struct{}](c, "/provider/widgets/delete", DeleteWidgetRequest{})
	CallFlowsAPI[DeleteWidgetRequest, struct{}](c, "/provider/widgets/confirm_widget", DeleteWidgetRequest{})
	CallFlowsAPI[AttachRequest, struct{}](c, "/provider/widgets/attach_to_flow", AttachRequest{})
	CallFlowsAPI[AttachRequest, struct{}](c, "/provider/widgets/detach_from_flow", AttachRequest{})
	CallFlowsAPI[ExportRequest, ExportResponse](c, "/provider/flows/export_definition", ExportRequest{})
	CallFlowsAPI[SecretRequest, struct{}](c, "/provider/organization/read_secret", SecretRequest{})
	CallFlowsAPI[ListProjectsRequest, ListProjectsResponse](c, "/provider/projects/list", ListProjectsRequest{})
	CallFlowsAPI[FindRequest, struct{}](c, "/provider/widgets/find", FindRequest{})
}
`

const flowctlFixture = `import { getAuthHeaders } from "./auth.ts";

async function apiFetch(path: string, body: any) {
  const res = await fetch(path, { method: "POST", body: JSON.stringify(body) });
  return res.json();
}

/* Options for an export. */
export interface ExportOptions {
  omitIds?: boolean;
  readonly omitLayout?: boolean;
}

export type RunStatus = "queued" | "passed" | 'failed';

export interface Run {
  id: string;
  status: RunStatus;
  error: string | null;
  labels: Record<string, string>;
  steps: Array<{ name: string; ok: boolean }>;
  scores: number[];
  "quoted-key"?: unknown;
}

// Lists projects; the response is not enveloped.
export async function getProjects() {
  const data = await apiFetch("/cli/projects", {});
  return data.projects as Array<{ id: string; name: string }>;
}

export async function getWidgets(projectId: string) {
  const data = await apiFetch("/cli/widgets/list_widgets", { projectId });
  return data.data.widgets;
}

export async function createWidget(name: string, color: string, projectId: string) {
  const res = await apiFetch("/cli/widgets/create_widget", {
    name,
    projectId,
    style: { color: color, size: 3, round: true, label: "x" },
  });
  return res.data.widget.id;
}

export async function publish(widgetId: string): Promise<void> {
  await apiFetch("/cli/widgets/update", { id: widgetId, draft: false });
}

export async function rename(widgetId: string, name: string) {
  await apiFetch("/cli/widgets/update", { id: widgetId, name });
}

export async function exportFlow(flowId: string, options: ExportOptions = {}) {
  const res = await apiFetch("/cli/flows/export", { flowId, ...options });
  return res.data.data as string;
}

export async function startRun(scenarioId: string) {
  const res = await apiFetch("/cli/flows/start_test_scenario_run", { scenarioId });
  return res.data as { id: string; runs: Run[] };
}

export async function startRuns(flowId: string) {
  const res = await apiFetch("/cli/flows/start_test_scenario_run_batch", { flowId });
  return res?.data.ids as string[];
}

export async function cancelRun(runId: string) {
  await apiFetch("/cli/flows/cancel_test_scenario_run", { id: runId });
}
`

const flowsSDKFixture = `/**
 * A pair.
 */
interface KVPair {
    key: string;
    value?: any;
}
interface AppSchema {
    name?: string;
    readonly blocks: Record<string, AppBlock>;
    onSync?: (input: { a: string; b: number }) => Promise<void>;
    "quoted": boolean;
    generic<T>(x: T): T;
}
type Status = "ready" | "failed";
type Shape = {
    type: "cron";
    cron: { expression: string };
};
declare const kv: {
    get: (key: string) => Promise<KVPair>;
    list(input: { prefix: string }): Promise<KVPair[]>;
};
declare const plain: number;
declare function defineApp(app: AppSchema): AppSchema;
export { defineApp, kv };
export type { AppSchema, Status };
`

const flowsMappingFixture = `
tags:
  Flows Widgets: Widgets.
  Flows Projects: Projects and their secrets.
  Flows Flows: Flows.
  Flows Test Scenarios: Test scenarios.
resources:
  project: {collection: projects, id: projectId, tag: Flows Projects}
  secret: {collection: secrets, id: key, parent: project, tag: Flows Projects}
  widget: {collection: widgets, id: widgetId, tag: Flows Widgets}
  flow: {collection: flows, id: flowId, tag: Flows Flows}
  testScenario: {collection: test-scenarios, id: testScenarioId, keys: [scenarioId], tag: Flows Test Scenarios}
  testScenarioRun: {collection: test-scenario-runs, id: testScenarioRunId, tag: Flows Test Scenarios}
nouns:
  project: project
  secret: secret
  widget: widget
  flow: flow
  test_scenario_run: testScenarioRun
rpcs:
  /provider/projects/list: {operationId: listProjects}
  /cli/projects: {operationId: listProjects, method: GET, path: /flows/projects, resource: project}
  /provider/organization/read_secret: {operationId: getSecret}
  /provider/widgets/create: {operationId: createWidget}
  /cli/widgets/create_widget: {operationId: createWidgetDraft, tag: Flows Projects, method: POST, path: /flows/widget-drafts, resource: widget}
  /cli/widgets/list_widgets: {operationId: listWidgets}
  /provider/widgets/get: {operationId: getWidget}
  /provider/widgets/update: {operationId: updateWidget}
  /cli/widgets/update: {operationId: updateWidget}
  /provider/widgets/delete: {operationId: deleteWidget}
  /provider/widgets/confirm_widget: {operationId: confirmWidget}
  /provider/widgets/attach_to_flow: {operationId: attachWidgetToFlow}
  /provider/widgets/detach_from_flow: {operationId: detachWidgetFromFlow}
  /provider/widgets/find: {operationId: findWidget, method: GET, path: /flows/widgets, resource: widget}
  /provider/flows/export_definition: {operationId: getFlowDefinition, method: GET, path: '/flows/flows/{flowId}/definition', resource: flow}
  /cli/flows/export: {operationId: getFlowDefinition, method: GET, path: '/flows/flows/{flowId}/definition', resource: flow}
  /cli/flows/start_test_scenario_run: {operationId: startTestScenarioRun}
  /cli/flows/start_test_scenario_run_batch: {operationId: startTestScenarioRuns}
exclude:
  /cli/flows/cancel_test_scenario_run: Cancelling is outside the fixture.
yaml:
  definition: FlowDefinition
`

const flowsOverlayFixture = `
FlowDefinition:
  type: object
  additionalProperties: false
  properties:
    blocks:
      type: array
      items: {$ref: '#/components/schemas/FlowBlock'}
FlowBlock:
  type: object
  additionalProperties: false
  required: [name]
  properties:
    name: {type: string}
    config: {type: object}
RegistryManifest:
  type: object
  required: [apps]
  properties:
    apps:
      type: object
      additionalProperties:
        type: object
        required: [appMetadataUrl]
        properties:
          appMetadataUrl: {type: string}
AppVersions:
  type: object
  required: [versions]
  properties:
    versions: {type: array, items: {type: object}}
`

const flowsLockFixture = `provider:
  repository: o/terraform-provider-flows
  ref: 1111111111111111111111111111111111111111
  sha256: aa
flowctl:
  repository: o/flowctl
  ref: 2222222222222222222222222222222222222222
  sha256: bb
sdk:
  package: '@o/sdk'
  version: 1.2.3
  sha256: cc
templates:
  index: https://example.test/templates.json
  flows:
    one: dd
registry:
  manifest: https://registry.example.test/core
fetchedAt: "2026-11-04T07:00:00Z"
`

func flowsProviderTarball(t *testing.T) []byte {
	t.Helper()
	return tarball(t, map[string]string{
		"internal/provider/client.go":       flowsProviderClient,
		"internal/provider/widgets.go":      flowsProviderFlows,
		"internal/provider/widgets_test.go": "not go",
		"main.go":                           "not read",
	})
}

// writeFlowsFixtureDir writes a Flows reference directory from the fixture
// clients, as a fetch would, with the fixture mapping and overlay.
func writeFlowsFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	provider, err := ReadFlowsProviderTarball(flowsProviderTarball(t))
	if err != nil {
		t.Fatal(err)
	}
	flowctl, err := ReadFlowctlAPI([]byte(flowctlFixture))
	if err != nil {
		t.Fatal(err)
	}
	ids, err := ReadSDKIdentifiers([]byte(flowsSDKFixture))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		FileLock:         flowsLockFixture,
		FileMapping:      flowsMappingFixture,
		FileFlowsOverlay: flowsOverlayFixture,
	}
	for name, v := range map[string]any{FileFlowsProvider: provider, FileFlowsFlowctl: flowctl, FileFlowsSDK: ids} {
		b, err := marshalJSON(v)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(b)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), filePerm); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func generateFlowsFixture(t *testing.T) *Generated {
	t.Helper()
	g, err := Flows.GenerateDir(context.Background(), writeFlowsFixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

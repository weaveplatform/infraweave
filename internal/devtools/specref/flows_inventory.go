package specref

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The SDK register is docs/data/flows-sdk-inventory.json: one record per
// declaration of @slflows/sdk's index.d.ts, with its member names. It feeds
// no REST operation; it is what the app protocol (pkg/flows/app/v1) is
// checked against. The SDK states no licence, so the register holds
// identifiers only, never the declarations' types or comments.

// flowsSDKRegister is the register's document; its shape is
// docs/data/schema/flows-sdk-inventory.schema.json.
type flowsSDKRegister struct {
	//nolint:tagliatelle // $schema is the register convention of docs/data
	Schema  string          `json:"$schema"`
	Package string          `json:"package"`
	Version string          `json:"version"`
	Records []SDKIdentifier `json:"records"`
}

const flowsSDKSchema = "schema/flows-sdk-inventory.schema.json"

// flowsInventory writes, or with check verifies, the SDK register from the
// identifiers snapshot and the lock in dir. Flows has no inventory report,
// so out.Report must be empty.
func flowsInventory(dir string, out InventoryOutputs, check bool) error {
	if out.Report != "" {
		return fmt.Errorf("%w: the Flows inventory has no report; drop --out", ErrFlowsMapping)
	}
	if out.Register == "" {
		return fmt.Errorf("%w: the Flows inventory needs --register", ErrFlowsMapping)
	}
	want, err := flowsRegister(dir)
	if err != nil {
		return err
	}
	if !check {
		if err := os.WriteFile(out.Register, want, filePerm); err != nil {
			return fmt.Errorf("write %s: %w", out.Register, err)
		}
		return nil
	}
	got, err := os.ReadFile(filepath.Clean(out.Register))
	if err != nil || !bytes.Equal(got, want) {
		return fmt.Errorf("%w: %s (run specref inventory --product flows)", ErrDrift, out.Register)
	}
	return nil
}

func flowsRegister(dir string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open reference dir: %w", err)
	}
	defer root.Close()
	data, err := root.ReadFile(FileFlowsSDK)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", FileFlowsSDK, err)
	}
	reg := flowsSDKRegister{Schema: flowsSDKSchema}
	if err := json.Unmarshal(data, &reg.Records); err != nil {
		return nil, fmt.Errorf("decode %s: %w", FileFlowsSDK, err)
	}
	if data, err = root.ReadFile(FileLock); err != nil {
		return nil, fmt.Errorf("read %s: %w", FileLock, err)
	}
	var lock FlowsLock
	if err := strictYAML(data, &lock); err != nil {
		return nil, fmt.Errorf("%s: %w", FileLock, err)
	}
	reg.Package, reg.Version = lock.SDK.Package, lock.SDK.Version
	return marshalJSON(reg)
}

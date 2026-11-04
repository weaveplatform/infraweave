package stubgen

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// OperationIDs maps "METHOD /path" to operationId for every operation in an
// OpenAPI document, so a stub names the operation as the spec does rather
// than as oapi-codegen spells its Go method.
func OperationIDs(spec []byte) (map[string]string, error) {
	// Path items also hold parameters, summary and the like, so each value
	// is decoded only when its key is an HTTP method.
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("stubgen: parse spec: %w", err)
	}
	ids := map[string]string{}
	for path, item := range doc.Paths {
		for method, node := range item {
			if !slices.Contains(httpMethods, method) {
				continue
			}
			var op struct {
				OperationID string `yaml:"operationId"`
			}
			if err := node.Decode(&op); err != nil {
				return nil, fmt.Errorf("stubgen: %s %s: %w", method, path, err)
			}
			if op.OperationID != "" {
				ids[strings.ToUpper(method)+" "+path] = op.OperationID
			}
		}
	}
	return ids, nil
}

package harness

import (
	"fmt"
	"maps"

	"gopkg.in/yaml.v3"
)

// SecretsWith is the secrets settings with kek as secrets.kek; nil
// selects the local KEK, which StartServer passes in KEK.
func SecretsWith(kek map[string]any) map[string]any {
	if kek == nil {
		kek = map[string]any{"provider": "local"}
	}
	return map[string]any{"kek": kek}
}

// objectStore is the fs driver under root. Replicas of one scenario share
// the root, as replicas share a volume. The URL names no listener, since
// a scenario's ports are known only once its servers start; the
// signed-URL steps sign for the listener themselves.
func objectStore(root string) map[string]any {
	return map[string]any{
		"driver": "fs",
		"fs":     map[string]any{"root": root, "url": PublicURL},
	}
}

// PublicURL is every server's server.public_url, which the modes that
// serve the API require; no client dials it.
const PublicURL = "http://infraweave.test"

// ServerConfig is a strict configuration file for one server: both
// listeners on loopback port 0, the database at dsn, the local KEK, object
// storage under objects, and the server settings the scenario names over
// them. The ready delay is 0 unless the scenario names one, so stopping a
// server costs no wait.
func ServerConfig(dsn, objects string, settings map[string]any) ([]byte, error) {
	server := map[string]any{
		"listen":       "127.0.0.1:0",
		"admin_listen": "127.0.0.1:0",
		"shutdown":     map[string]any{"ready_delay": "0s"},
		"public_url":   PublicURL,
	}
	maps.Copy(server, settings)
	out, err := yaml.Marshal(map[string]any{
		"server":      server,
		"database":    map[string]any{"url": dsn},
		"secrets":     SecretsWith(nil),
		"objectstore": objectStore(objects),
		"vendors":     offlineIndexes,
	})
	if err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	return out, nil
}

// offlineIndexes are release indexes on a closed local port, which every
// server reads unless its scenario serves the recorded ones: no
// acceptance run reaches the vendors' real indexes, and a refresh that
// cannot read them keeps the empty catalogue.
var offlineIndexes = map[string]any{
	"terraform_index": "http://127.0.0.1:1/terraform",
	"opentofu_index":  "http://127.0.0.1:1/opentofu",
}

// withSection is config with its top-level key set to section.
func withSection(config []byte, key string, section map[string]any) ([]byte, error) {
	var m map[string]any
	if err := yaml.Unmarshal(config, &m); err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	m[key] = section
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	return out, nil
}

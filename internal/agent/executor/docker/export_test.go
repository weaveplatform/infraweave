package docker

// API is the moby client's part the executor calls, for a fake daemon.
type API = api

// NewWith is an executor over cli that mounts binary.
func NewWith(cli API, binary string, c Config) *Executor { return newExecutor(cli, binary, c) }

package objectstoretest

import (
	"context"
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go"

	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/test/deps"
)

// Emulator starts image for t with opts and returns the HTTP endpoint of
// its port, such as "10000/tcp". Without a Docker daemon t skips, unless
// sqltest.RequireDocker is 1, as CI sets it, so an emulator suite never
// passes there by skipping.
func Emulator(t *testing.T, image, port string, opts ...testcontainers.ContainerCustomizer) string {
	t.Helper()
	if err := deps.DockerReachable(t.Context()); err != nil {
		if os.Getenv(sqltest.RequireDocker) != "1" {
			t.Skipf("%s: %v (set %s=1 to fail instead)", image, err, sqltest.RequireDocker)
		}
		t.Fatalf("%s: %v", image, err)
	}
	c, err := testcontainers.Run(t.Context(), image, append([]testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts(port),
	}, opts...)...)
	if c != nil {
		// t.Context has ended by the time cleanups run.
		t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	}
	if err != nil {
		t.Fatalf("%s: %v", image, err)
	}
	endpoint, err := c.PortEndpoint(t.Context(), port, "http")
	if err != nil {
		t.Fatalf("%s: %v", image, err)
	}
	return endpoint
}

package kubernetes

import (
	"context"
	"fmt"
	"io"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/streaming/pkg/httpstream"
)

// Streams opens a Pod container's attach and exec streams, both without a
// terminal: Attach connects to the container's own standard streams, and
// Exec runs command beside its root process. Each answers once the
// stream ends: when the container or command exits, or ctx ends.
type Streams interface {
	Attach(ctx context.Context, namespace, pod, container string, stdin io.Reader, stdout, stderr io.Writer) error
	Exec(ctx context.Context, namespace, pod, container string, command []string, stdout, stderr io.Writer) error
}

// apiStreams opens streams through the Kubernetes API server, over
// WebSocket, or SPDY where the server refuses the WebSocket upgrade.
type apiStreams struct {
	config *rest.Config
	rest   rest.Interface
	codec  runtime.ParameterCodec
}

// NewStreams answers Streams through the API server config reaches,
// whose core REST client is core.
func NewStreams(config *rest.Config, core rest.Interface) Streams {
	scheme := runtime.NewScheme()
	// AddToScheme fails only for conflicting registrations, which a new
	// scheme of one group does not have.
	_ = corev1.AddToScheme(scheme)
	return &apiStreams{config: config, rest: core, codec: runtime.NewParameterCodec(scheme)}
}

func (s *apiStreams) Attach(ctx context.Context, namespace, pod, container string, stdin io.Reader, stdout, stderr io.Writer) error {
	req := s.rest.Post().Namespace(namespace).Resource("pods").Name(pod).SubResource("attach").
		VersionedParams(&corev1.PodAttachOptions{
			Container: container, Stdin: stdin != nil, Stdout: stdout != nil, Stderr: stderr != nil,
		}, s.codec)
	return s.stream(ctx, req, stdin, stdout, stderr)
}

func (s *apiStreams) Exec(ctx context.Context, namespace, pod, container string, command []string, stdout, stderr io.Writer) error {
	req := s.rest.Post().Namespace(namespace).Resource("pods").Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container, Command: command, Stdout: stdout != nil, Stderr: stderr != nil,
		}, s.codec)
	return s.stream(ctx, req, nil, stdout, stderr)
}

func (s *apiStreams) stream(ctx context.Context, req *rest.Request, stdin io.Reader, stdout, stderr io.Writer) error {
	url := req.URL()
	ws, err := remotecommand.NewWebSocketExecutor(s.config, http.MethodGet, url.String())
	if err != nil {
		return fmt.Errorf("kubernetes: open a stream: %w", err)
	}
	spdy, err := remotecommand.NewSPDYExecutor(s.config, http.MethodPost, url)
	if err != nil {
		return fmt.Errorf("kubernetes: open a stream: %w", err)
	}
	ex, err := remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return fmt.Errorf("kubernetes: open a stream: %w", err)
	}
	if err := ex.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: stdout, Stderr: stderr}); err != nil {
		return fmt.Errorf("kubernetes: stream: %w", err)
	}
	return nil
}

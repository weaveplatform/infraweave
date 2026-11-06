package objectstoretest

import (
	"net/http"
	"strings"
	"testing"
)

// md5Headers are where S3, Cloud Storage and Blob Storage carry an MD5.
var md5Headers = []string{"Content-Md5", "X-Ms-Blob-Content-Md5", "X-Ms-Content-Md5"}

// NoMD5 returns a client over rt that fails t when a request carries an
// MD5, which FIPS 140-only mode refuses (decision 0013).
func NoMD5(t *testing.T, rt http.RoundTripper) *http.Client {
	return &http.Client{Transport: noMD5{t, rt}}
}

type noMD5 struct {
	t  *testing.T
	rt http.RoundTripper
}

func (n noMD5) RoundTrip(req *http.Request) (*http.Response, error) {
	for _, h := range md5Headers {
		if req.Header.Get(h) != "" {
			n.t.Errorf("%s %s sends %s", req.Method, req.URL.Path, h)
		}
	}
	for _, v := range req.Header.Values("X-Goog-Hash") {
		if strings.Contains(v, "md5=") {
			n.t.Errorf("%s %s sends an MD5 in x-goog-hash", req.Method, req.URL.Path)
		}
	}
	return n.rt.RoundTrip(req) //nolint:wrapcheck // the transport's own error
}

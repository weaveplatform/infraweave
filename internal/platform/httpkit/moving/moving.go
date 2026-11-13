package moving

import (
	"io"
	"net/http"
	"time"
)

// Stall is how long a verified transfer may go without moving a byte
// before its connection is cut. An object, such as a state file or a
// workspace, may take longer to move than any fixed timeout of the
// listener allows, so a transfer's deadlines move with each step instead.
//
// ref: decision 0003 § "API surfaces"
const Stall = 30 * time.Second

// Deadlines moves a verified transfer's deadlines to stall past now with
// each step: an upload's read deadline, and the write deadline of the
// answer that follows its body; a download's write deadline, since
// net/http clears the read deadline once a request's body is read. A
// writer whose deadlines cannot move keeps the listener's.
type Deadlines struct {
	rc     *http.ResponseController
	stall  time.Duration
	upload bool
}

// Start starts the first step of w's transfer, an upload when upload is
// set and a download otherwise, whose steps may each take stall.
func Start(w http.ResponseWriter, stall time.Duration, upload bool) Deadlines {
	d := Deadlines{rc: http.NewResponseController(w), stall: stall, upload: upload}
	d.Step()
	return d
}

// Step moves the deadlines to stall past now.
func (d Deadlines) Step() {
	until := time.Now().Add(d.stall)
	if d.upload {
		_ = d.rc.SetReadDeadline(until)
	}
	_ = d.rc.SetWriteDeadline(until)
}

// Reader is body with each read a step.
func (d Deadlines) Reader(body io.ReadCloser) io.ReadCloser { return reader{body, d} }

// Writer is w with each write a step.
func (d Deadlines) Writer(w http.ResponseWriter) http.ResponseWriter { return writer{w, d} }

type reader struct {
	io.ReadCloser
	d Deadlines
}

func (r reader) Read(p []byte) (int, error) {
	r.d.Step()
	return r.ReadCloser.Read(p) //nolint:wrapcheck // the body's own error, as io.Reader returns it
}

type writer struct {
	http.ResponseWriter
	d Deadlines
}

func (w writer) Write(p []byte) (int, error) {
	w.d.Step()
	return w.ResponseWriter.Write(p) //nolint:wrapcheck // as above
}

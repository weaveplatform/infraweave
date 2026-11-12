package harness

import (
	"net/http"
	"sync"
)

// Jar is the scenario's browser: an sdk.Doer that sends the cookies it
// holds and keeps those the server sets. net/http/cookiejar would send a
// Secure cookie only over https, and the servers listen on plain http, so
// the Jar holds them itself. It follows no redirect, so a step sees the
// sign-in's 303.
type Jar struct {
	mu sync.Mutex
	// values are the live cookies by name, and set the Set-Cookie each
	// last arrived in.
	values map[string]string
	set    map[string]*http.Cookie
	// ended are the values a removal or a replacement ended.
	ended  map[string]string
	client *http.Client
}

func NewJar() *Jar {
	return &Jar{
		values: map[string]string{},
		set:    map[string]*http.Cookie{},
		ended:  map[string]string{},
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

func (j *Jar) Do(req *http.Request) (*http.Response, error) {
	j.mu.Lock()
	for name, value := range j.values {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	j.mu.Unlock()
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, err //nolint:wrapcheck // the SDK names the request
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range resp.Cookies() {
		if old, ok := j.values[c.Name]; ok && old != c.Value {
			j.ended[c.Name] = old
		}
		if c.MaxAge < 0 {
			delete(j.values, c.Name)
		} else {
			j.values[c.Name] = c.Value
		}
		j.set[c.Name] = c
	}
	return resp, nil
}

// Hold makes the jar hold value as the session cookie, as a sign-in seeded
// in SQL would have left it.
func (j *Jar) Hold(value string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.values[sessionCookie] = value
}

// SetCookie is the Set-Cookie that last set the session cookie, or nil.
func (j *Jar) SetCookie() *http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.set[sessionCookie]
}

// EndedSession is the session cookie's value a removal or a replacement
// last ended, or "".
func (j *Jar) EndedSession() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.ended[sessionCookie]
}

func (j *Jar) Session() (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	v, ok := j.values[sessionCookie]
	return v, ok
}

// sessionCookie is the browser session's cookie.
//
// ref: decision 0006
const sessionCookie = "__Host-infraweave_session"

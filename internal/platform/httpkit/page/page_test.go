package page

import (
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

type key struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
}

func TestUnitRoundTrip(t *testing.T) {
	want := kernel.Cursor[key]{
		Key: key{Name: "prod/eu", Created: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)},
		ID:  "01HZ0000000000000000000000",
	}
	c, err := Encode("stacks", want)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(c, "+/=") {
		t.Errorf("cursor %q is not URL-safe", c)
	}
	got, err := Decode[key]("stacks", c)
	if err != nil || got != want {
		t.Errorf("Decode = %+v, %v; want %+v", got, err, want)
	}
	s, err := Encode("runs", kernel.Cursor[string]{Key: "b", ID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Decode[string]("runs", s); err != nil || got.Key != "b" {
		t.Errorf("string key: %+v %v", got, err)
	}
}

func TestUnitEncodeError(t *testing.T) {
	if _, err := Encode("stacks", kernel.Cursor[float64]{Key: math.NaN(), ID: "1"}); err == nil {
		t.Error("an unencodable key was accepted")
	}
}

func raw(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func TestUnitTampered(t *testing.T) {
	good, _ := Encode("stacks", kernel.Cursor[string]{Key: "a", ID: "1"})
	for name, c := range map[string]string{
		"not base64":     "!!!",
		"padded base64":  base64.URLEncoding.EncodeToString([]byte(`{"v":1}`)),
		"not JSON":       raw("nope"),
		"truncated":      good[:len(good)-3],
		"trailing data":  raw(`{"v":1,"l":"stacks","k":"a","id":"1"}{}`),
		"unknown field":  raw(`{"v":1,"l":"stacks","k":"a","id":"1","x":1}`),
		"other version":  raw(`{"v":2,"l":"stacks","k":"a","id":"1"}`),
		"other list":     raw(`{"v":1,"l":"runs","k":"a","id":"1"}`),
		"no ID":          raw(`{"v":1,"l":"stacks","k":"a"}`),
		"no key":         raw(`{"v":1,"l":"stacks","id":"1"}`),
		"key wrong type": raw(`{"v":1,"l":"stacks","k":7,"id":"1"}`),
		"empty":          "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode[string]("stacks", c)
			pe, ok := errors.AsType[*problem.Error](err)
			if !ok || pe.Problem.Status != http.StatusBadRequest ||
				pe.Problem.Type != problem.TypePrefix+"invalid-cursor" {
				t.Errorf("got %v, want an invalid-cursor problem", err)
			}
		})
	}
}

package stream

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"
)

type chunks struct{ n int }

func (c *chunks) Read(p []byte) (int, error) {
	c.n++
	if c.n == 1 {
		return copy(p, "data: a"), nil
	}
	if c.n == 2 {
		return copy(p, "\n\n"), nil
	}
	return 0, io.EOF
}

type recorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *recorder) Flush() { r.flushes++; r.ResponseRecorder.Flush() }
func TestRelayFlushesPartialFrames(t *testing.T) {
	w := &recorder{ResponseRecorder: httptest.NewRecorder()}
	got := Relay(context.Background(), w, &chunks{}, time.Now())
	if got.Err != nil || got.Bytes != 9 || w.flushes != 2 || w.Body.String() != "data: a\n\n" {
		t.Fatalf("%+v flush=%d body=%q", got, w.flushes, w.Body.String())
	}
}
func TestRelayReportsReadErrorAfterData(t *testing.T) {
	r := io.MultiReader(io.LimitReader(&chunks{}, 7), errReader{})
	got := Relay(context.Background(), httptest.NewRecorder(), r, time.Now())
	if !got.Started || got.Err == nil {
		t.Fatalf("%+v", got)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("reset") }

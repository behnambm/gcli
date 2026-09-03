// === Behavioral Contract: cmd dispatch plumbing ===
//   - exitCode: config errors → 1; HTTP 401 → 3; 403/404 → 4; query errors
//     → 5; everything else → 2
//   - hintOf: HTTPErrors and QueryErrors produce guidance; plain errors do not
//   - shouldRender: results render when there is no error or when partial
//     frames exist; an error with no frames renders nothing (the error is
//     the output)
package cmd

import (
	"errors"
	"testing"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/config"
	"github.com/behnambm/gcli/internal/frames"
)

func TestExitCode_mapsErrorClasses(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{errors.New("plain"), 2},
		{&api.HTTPError{StatusCode: 401}, 3},
		{&api.HTTPError{StatusCode: 403}, 4},
		{&api.HTTPError{StatusCode: 404}, 4},
		{&api.HTTPError{StatusCode: 500}, 2},
		{&api.QueryError{RefID: "A"}, 5},
		{config.ErrMissingURL, 1},
		{config.ErrMissingToken, 1},
	}
	for _, tc := range cases {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestHintOf_knownErrors_produceGuidance(t *testing.T) {
	if got := hintOf(&api.HTTPError{StatusCode: 401, Body: ""}); got == "" {
		t.Error("401 must have hint")
	}
	if got := hintOf(&api.QueryError{RefID: "A", Msg: "x"}); got == "" {
		t.Error("QueryError must have hint")
	}
}

func TestHintOf_plainErrors_haveNoHint(t *testing.T) {
	if got := hintOf(errors.New("plain")); got != "" {
		t.Errorf("plain error hint = %q, want empty", got)
	}
}

func TestShouldRender_noError_renders(t *testing.T) {
	r := result{res: frames.Result{Frames: nil}}
	if !shouldRender(nil, r) {
		t.Error("successful result must render (meta/frames envelope)")
	}
}

func TestShouldRender_partialFramesWithError_renders(t *testing.T) {
	r := result{res: frames.Result{Frames: []frames.Frame{{RefID: "B"}}}}
	if !shouldRender(errors.New("query A failed"), r) {
		t.Error("partial frames must render alongside the error")
	}
}

func TestShouldRender_errorWithoutFrames_doesNotRender(t *testing.T) {
	if shouldRender(errors.New("query failed"), result{}) {
		t.Error("an error with no frames must not render — the error is the output")
	}
}

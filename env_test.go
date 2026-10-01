package docsync

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestWithCommandName pins that the option reaches the system (bug 126); the
// cli's TestCustomNameReachesEveryMessage proves the remedies it changes.
func TestWithCommandName(t *testing.T) {
	t.Parallel()
	if s := newSys(t, repo(false), WithCommandName("pds")); s.command != "pds" {
		t.Errorf("command = %q, want pds", s.command)
	}
}

// TestEnvFlagIsHeldToEnvKnown pins bug 120's flag half: an environment asked
// for by name -- check's and render's --env, def's --env -- must be one
// [env] known lists when it lists any, and is refused naming the list
// otherwise. A listed name, or no name at all, runs as before, so the test
// fails if the guard refuses everything as well as if it refuses nothing.
// promise:env-known-closed
func TestEnvFlagIsHeldToEnvKnown(t *testing.T) {
	t.Parallel()
	c := cfg()
	c.Env.Known = []string{"prod", "staging"}
	s := newSys(t, repo(false), WithConfig(c))
	ctx := context.Background()

	if _, err := s.Check(ctx, CheckOptions{Env: "stagng"}); !errors.Is(err, ErrUnknownEnv) || !strings.Contains(err.Error(), "prod, staging") {
		t.Errorf("check --env stagng = %v, want ErrUnknownEnv naming the list", err)
	}
	if _, _, err := s.Render(ctx, "docs/sessions.md", RenderOptions{Env: "stagng"}); !errors.Is(err, ErrUnknownEnv) {
		t.Errorf("render --env stagng = %v, want ErrUnknownEnv", err)
	}
	if _, err := s.Define(ctx, "notes.txt:1", DefineOptions{Env: "stagng"}); !errors.Is(err, ErrUnknownEnv) {
		t.Errorf("def --env stagng = %v, want ErrUnknownEnv", err)
	}
	for _, env := range []string{"", "staging"} {
		if _, err := s.Check(ctx, CheckOptions{Env: env}); err != nil {
			t.Errorf("check --env %q = %v, want it to run", env, err)
		}
	}
}

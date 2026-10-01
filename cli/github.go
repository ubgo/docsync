package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/check"
)

// `ds github comment` (§24 "pull request"): one comment per doc with the
// findings for that doc, each linked to the doc line, with the block diff
// folded underneath. A second run edits the same comments instead of
// adding more, and a doc whose findings cleared gets its comment marked
// resolved. The exit code is the check's, so a required status blocks the
// merge on errors, unless the pull request carries the ack label: then
// every finding is acked on behalf of the reviewer who applied the label
// (§24 "a docs-acked label lets a reviewer ack --all") and the command
// prints what to commit.
//
// GitHub is reached through its REST API with the environment the Actions
// runner sets: GITHUB_TOKEN, GITHUB_REPOSITORY, GITHUB_API_URL, and the
// event payload at GITHUB_EVENT_PATH for the pull request number, head
// sha, labels, and sender. Nothing here is git-host generic: this is the
// GitHub adapter, which is why it lives in the CLI and not the library.
const (
	envGitHubToken      = "GITHUB_TOKEN"
	envGitHubRepository = "GITHUB_REPOSITORY"
	envGitHubAPIURL     = "GITHUB_API_URL"
	envGitHubServerURL  = "GITHUB_SERVER_URL"
	defaultGitHubAPI    = "https://api.github.com"
	defaultGitHubServer = "https://github.com"
	// commentMarker identifies the comment for one doc across runs.
	commentMarker = "<!-- docsync:doc=%s -->"
	// commentPageSize is GitHub's maximum page; a PR with more docsync
	// comments than that has bigger problems.
	commentPageSize = 100
	flagReport      = "report"
	flagPR          = "pr"
	flagAckLabel    = "ack-label"
	defaultAckLabel = "docs-acked"
	// commentBudget is where a comment body stops adding findings, kept
	// well under GitHub's 65536-character limit so the last finding and
	// the "and N more" line still fit.
	commentBudget = 60000
	// actionLabeled is the pull_request event action for a label applied.
	actionLabeled = "labeled"
	actorGitHub   = "github:"
	acceptGitHub  = "application/vnd.github+json"
)

// ErrGitHubEnv names the missing environment.
var ErrGitHubEnv = errors.New("github comment needs GITHUB_TOKEN and GITHUB_REPOSITORY, and a pull request number (--pr or GITHUB_EVENT_PATH)")

// prEvent is the subset of the pull_request event payload used here.
type prEvent struct {
	// Action is what happened: "labeled" when a label was applied, and
	// "synchronize", "opened" and so on otherwise. Label is the label just
	// applied, set only on "labeled".
	Action string `json:"action"`
	Label  struct {
		Name string `json:"name"`
	} `json:"label"`
	PullRequest struct {
		Number int `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"pull_request"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

func (a *App) githubCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "github", Short: "GitHub integration: pull request comments"}
	var reportPath, ackLabel string
	var pr int
	var dry bool
	comment := &cobra.Command{
		Use:   "comment",
		Short: "post one comment per doc with the check's findings on the pull request",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			rep, err := a.reportFor(cmd.Context(), ld, reportPath)
			if err != nil {
				return err
			}
			var ev prEvent
			if p := os.Getenv(githubEventPath); p != "" {
				if raw, err := os.ReadFile(p); err == nil {
					_ = json.Unmarshal(raw, &ev)
				}
			}
			if pr == 0 {
				pr = ev.PullRequest.Number
			}
			gh := githubClient{client: a.httpClient, token: os.Getenv(envGitHubToken), repo: os.Getenv(envGitHubRepository), api: envOr(envGitHubAPIURL, defaultGitHubAPI), server: envOr(envGitHubServerURL, defaultGitHubServer)}
			if gh.client == nil {
				gh.client = &http.Client{Timeout: urlTimeout}
			}
			if gh.token == "" || gh.repo == "" || pr == 0 {
				return ErrGitHubEnv
			}
			sha := ev.PullRequest.Head.SHA
			if sha == "" {
				sha = rep.Commit
			}
			out := cmd.OutOrStdout()
			bodies := commentBodies(rep, gh.server+"/"+gh.repo, sha)
			for doc, body := range bodies {
				bodies[doc] = a.cmdText(body)
			}
			if dry {
				for _, doc := range sortedKeys(bodies) {
					fmt.Fprintf(out, "%s\n", bodies[doc])
				}
			} else {
				existing, err := gh.comments(cmd.Context(), pr)
				if err != nil {
					return err
				}
				if err := gh.upsert(cmd.Context(), pr, bodies, existing, out); err != nil {
					return err
				}
			}
			// Only the act of applying the label acks, credited to whoever
			// applied it. Acking whenever the label was present acked every
			// later push too — the sender of a synchronize event is whoever
			// pushed — so once a reviewer labelled a pull request, each new
			// change went through unreviewed under its author's name.
			applied := ackLabel != "" && ev.Action == actionLabeled && ev.Label.Name == ackLabel
			present := false
			for _, l := range ev.PullRequest.Labels {
				present = present || l.Name == ackLabel
			}
			if ackLabel != "" && present && !applied && rep.ExitCode != 0 {
				fmt.Fprintf(out, "label %s is on the pull request, but it acks only when it is applied: re-apply it to accept these findings\n", ackLabel)
			}
			if applied {
				n, err := a.ackAll(ld, rep, actorGitHub+ev.Sender.Login, fmt.Sprintf("label %s on pull request #%d", ackLabel, pr))
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%d findings acked under label %s; commit %s/%s\n", n, ackLabel, DirName, AcksFile)
				return nil
			}
			fmt.Fprintf(out, "%s\n", summaryLine(rep))
			if rep.ExitCode != 0 {
				return exitCode(rep.ExitCode)
			}
			return nil
		},
	}
	comment.Flags().StringVar(&reportPath, flagReport, "", "read a check --json report from this `file` instead of checking")
	comment.Flags().IntVar(&pr, flagPR, 0, "pull request number (default: from GITHUB_EVENT_PATH)")
	comment.Flags().StringVar(&ackLabel, flagAckLabel, defaultAckLabel, "label under which every finding is acked for the reviewer; empty disables")
	comment.Flags().BoolVar(&dry, flagDryRun, false, "print the comment bodies instead of posting")
	cmd.AddCommand(comment)
	return cmd
}

// reportFor reads a saved report or runs the check.
func (a *App) reportFor(ctx context.Context, ld loaded, path string) (docsync.Report, error) {
	if path == "" {
		return ld.sys.Check(ctx, docsync.CheckOptions{})
	}
	raw, err := os.ReadFile(a.userPath(path))
	if err != nil {
		return docsync.Report{}, err
	}
	var rep docsync.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return docsync.Report{}, fmt.Errorf("%s: %w", path, err)
	}
	return rep, nil
}

// ackAll acks every open finding that cites an id, as `ack --all` would
// for each doc, and returns how many.
func (a *App) ackAll(ld loaded, rep docsync.Report, actor, note string) (int, error) {
	var reqs []docsync.AckRequest
	for _, f := range rep.Findings {
		if f.ID == "" || (f.Severity != check.SeverityError && f.Severity != check.SeverityWarning) {
			continue
		}
		reqs = append(reqs, docsync.AckRequest{ID: f.ID, Doc: f.Doc, Line: f.Line, Actor: actor, Note: note})
	}
	if len(reqs) == 0 {
		return 0, nil
	}
	if len(rep.Scan.Refs) == 0 {
		// A report read from a file carries no scan; acks need the live
		// references to hash against.
		live, err := ld.sys.Check(context.Background(), docsync.CheckOptions{})
		if err != nil {
			return 0, err
		}
		rep = live
	}
	rows, err := a.recordAcks(ld, rep.Scan, reqs, false)
	return len(rows), err
}

// commentBodies renders one markdown comment per doc with findings, with
// the marker first so later runs find it.
func commentBodies(rep docsync.Report, repoURL, sha string) map[string]string {
	byDoc := map[string][]check.Finding{}
	for _, f := range rep.Findings {
		if f.Severity == check.SeverityError || f.Severity == check.SeverityWarning {
			byDoc[f.Doc] = append(byDoc[f.Doc], f)
		}
	}
	out := map[string]string{}
	for doc, fs := range byDoc {
		var b strings.Builder
		fmt.Fprintf(&b, commentMarker+"\n### docsync: `%s`\n\n", doc, doc)
		for i, f := range fs {
			// GitHub refuses a comment body past its size limit, which
			// failed the whole command — CI red over the length of a doc's
			// findings. The body stops short and says what it left out.
			if b.Len() > commentBudget {
				fmt.Fprintf(&b, "\n…and %d more; run `ds check` for the full list.\n", len(fs)-i)
				break
			}
			fmt.Fprintf(&b, "- [line %d](%s/blob/%s/%s#L%d) **%s**", f.Line, repoURL, sha, doc, f.Line, f.State)
			if f.ID != "" {
				fmt.Fprintf(&b, " `%s`", f.ID)
			}
			fmt.Fprintf(&b, ": %s\n", f.Message)
			if f.Remedy.IfStillTrue != "" {
				fmt.Fprintf(&b, "  - still true: `%s`; otherwise: %s\n", f.Remedy.IfStillTrue, f.Remedy.IfNot)
			} else if f.Remedy.Fix != "" {
				fmt.Fprintf(&b, "  - fix: %s\n", f.Remedy.Fix)
			}
			if f.Diff != "" {
				fmt.Fprintf(&b, "\n  <details><summary>block diff</summary>\n\n  ```diff\n%s\n  ```\n  </details>\n", indent(f.Diff, "  "))
			}
		}
		out[doc] = b.String()
	}
	return out
}

// githubClient is the little REST surface used: list, create, and edit
// issue comments.
type githubClient struct {
	client      *http.Client
	token, repo string
	api, server string
}

type ghComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

func (g githubClient) do(ctx context.Context, method, path string, body any, into any) error {
	var payload io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.api+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", acceptGitHub)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("github: %s %s: %w: %s", method, path, ghStatus(resp.StatusCode), strings.TrimSpace(string(raw)))
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			return fmt.Errorf("github: %s %s: %w", method, path, err)
		}
	}
	return nil
}

// ghStatus is an HTTP status GitHub answered with, as an error a caller can
// inspect with errors.As.
type ghStatus int

func (s ghStatus) Error() string { return fmt.Sprintf("%d %s", int(s), http.StatusText(int(s))) }

// notOurs reports a refusal to touch a comment this token cannot edit:
// anyone can post a comment that starts with docsync's marker, and editing
// it is forbidden (403) or, once deleted, not found (404).
func notOurs(err error) bool {
	var s ghStatus
	return errors.As(err, &s) && (s == http.StatusForbidden || s == http.StatusNotFound)
}

// maxBodyBytes bounds a GitHub response read.
const maxBodyBytes = 4 << 20

// comments returns the docsync comments on a pull request keyed by doc.
func (g githubClient) comments(ctx context.Context, pr int) (map[string]ghComment, error) {
	// Every page, not the first: on a busy pull request docsync's comment
	// sits past it, and a client that read one page never found it and
	// posted another copy on every push. A short page is the last one.
	var all []ghComment
	for page := 1; ; page++ {
		var batch []ghComment
		if err := g.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=%d&page=%d", g.repo, pr, commentPageSize, page), nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < commentPageSize {
			break
		}
	}
	out := map[string]ghComment{}
	for _, c := range all {
		if strings.HasPrefix(c.Body, "<!-- docsync:doc=") {
			doc := strings.TrimSuffix(strings.TrimPrefix(strings.SplitN(c.Body, "\n", 2)[0], "<!-- docsync:doc="), " -->")
			out[doc] = c
		}
	}
	return out, nil
}

// upsert posts new comments, edits existing ones, and marks cleared docs
// resolved.
func (g githubClient) upsert(ctx context.Context, pr int, bodies map[string]string, existing map[string]ghComment, out io.Writer) error {
	for _, doc := range sortedKeys(bodies) {
		body := bodies[doc]
		if c, ok := existing[doc]; ok {
			if c.Body == body {
				fmt.Fprintf(out, "%s: comment unchanged\n", doc)
				continue
			}
			err := g.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/comments/%d", g.repo, c.ID), map[string]string{"body": body}, nil)
			if err == nil {
				fmt.Fprintf(out, "%s: comment updated\n", doc)
				continue
			}
			if !notOurs(err) {
				return err
			}
			// A comment carrying the marker that this token may not edit is
			// someone else's. It used to fail the whole job; docsync posts
			// its own comment instead.
		}
		if err := g.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", g.repo, pr), map[string]string{"body": body}, nil); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: comment posted\n", doc)
	}
	for _, doc := range sortedKeys(existing) {
		if _, still := bodies[doc]; still {
			continue
		}
		resolved := fmt.Sprintf(commentMarker+"\n### docsync: `%s`\n\nResolved: no findings for this doc.\n", doc, doc)
		if existing[doc].Body == resolved {
			continue
		}
		if err := g.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/comments/%d", g.repo, existing[doc].ID), map[string]string{"body": resolved}, nil); err != nil {
			if notOurs(err) {
				continue // not docsync's comment to resolve
			}
			return err
		}
		fmt.Fprintf(out, "%s: comment resolved\n", doc)
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

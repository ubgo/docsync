# docsync GitHub Action

`uses: ubgo/docsync/integrations/github@main` runs `ds check --json` and, on a pull request, `ds github comment`: one comment per doc listing that doc's findings, each linked to the doc line, with the block diff folded underneath. Later runs edit the same comments; a doc whose findings cleared is marked resolved. The step exits with the check's code, so a required status blocks the merge on errors.

A reviewer who applies the `docs-acked` label (input `ack-label`) makes the next run ack every finding on their behalf; the workflow then commits `.ds/acks.tsv` or the reviewer does. Pull requests from forks never run `--run` or `--resolve`; `ds` refuses them itself. Only the run for the `labeled` event acks, credited to whoever applied the label; a push made afterwards is reviewed again, and the label must be re-applied to accept its findings.

The `workflows/` directory holds the three jobs from spec §24 ready to copy: pull request, publish after merge, and the nightly scheduled job that alone enables `--run` and `--resolve`.

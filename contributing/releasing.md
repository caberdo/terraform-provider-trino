# Releasing the provider

This is the maintainer runbook for shipping a release. A release is a pull
request that moves `VERSION` and cuts the changelog; merging it starts the
`Create Release Tag` workflow, which signs the tag, and the `Release` workflow,
which publishes with GoReleaser. This runbook covers that path end to end, plus
the manual dispatch and recovery steps for the cases a merge cannot cover.

The example version in every command is `0.2.1`. Substitute the version you are
shipping; the tag always carries a `v` prefix. Run the commands from a clone of
the repository with `gh` authenticated as a maintainer.

You need write access to the repository. The repository also needs the two
secrets the release workflows use: `GPG_PRIVATE_KEY` and `PASSPHRASE`. The
signing key must be registered to the account that owns the tagger email,
otherwise GitHub shows the tag as unverified even though the signature is
valid.

## 1. Pre-flight: open the release pull request

Start from a clean `main` and branch off it:

```bash
git switch main
git pull --ff-only
git switch -c release/0.2.1
```

Decide the version from the [patch-or-minor table](#6-patch-or-minor) below. A
release pull request changes three files consistently, plus any regenerated
reference under `docs/`:

1. `VERSION` — the version without the `v` prefix, for example `0.2.1`. This is
   the release trigger: only a merge that changes it starts a release.
2. `CHANGELOG.md` — move everything under `## Unreleased` into a new
   `## [0.2.1] - YYYY-MM-DD` section, leaving the `## Unreleased` heading in
   place.
3. `README.md` — the status line and, at a minor release, the install
   constraint (`~> 0.2`). A patch release leaves the constraint alone.

Confirm the target tag does not exist. The first command must print nothing and
the second must exit non-zero:

```bash
git ls-remote --tags origin "refs/tags/v0.2.1"
git rev-parse --verify "refs/tags/v0.2.1"
```

Run the release gates locally and prove the generated reference is clean:

```bash
make build vet lint
make coverage
make coverage-gate
make coverage-readme-check
make release-check
make validate-examples
make generate
git diff --exit-code
```

`make release-check` enforces the three-file agreement above. It also runs in the
`Test` job, so a mismatch fails the pull request instead of the tag workflow
after the merge.

`git diff --exit-code` must be silent. `make generate` rewrites `docs/`, so a
dirty tree means the committed reference is stale. Commit the changelog and any
regenerated docs on the release branch.

Acceptance tests are not part of that local loop unless you have Docker. CI
covers them: a pull request runs the declared floor and the newest supported
release, and the merge queue below runs the whole window from
`.github/trino-versions.txt` against the prospective merge commit. Run
`make testacc-docker` locally when the change touches provider behaviour.

## 2. Merge through the queue

`main` is protected by the `main-protection` ruleset: a pull request is
required, history is linear, force-pushes are blocked, and the merge queue is
enabled. The required contexts are `Lint & Security` and `Test`; the ruleset has
no bypass actors.

Repository-level auto-merge is disabled, so the usual CLI path fails:
`gh pr merge 55 --squash --auto` reports
`Auto merge is not allowed for this repository.`

The merge queue is still the supported path. `gh pr merge` has no flag for it;
enqueue the pull request through the GraphQL mutation instead:

```bash
PR=55
gh api graphql -f query='
  mutation($id: ID!) {
    enqueuePullRequest(input: {pullRequestId: $id}) {
      mergeQueueEntry { position }
    }
  }' -f id="$(gh pr view "$PR" --json id --jq .id)"
```

The response reports the queue position. `enqueuePullRequest` is the queue's own
API, so it works without turning on the repository auto-merge setting. Do not
enable auto-merge or add a bypass actor to work around the error: that weakens
the gate for every later release.

Once enqueued, GitHub builds the prospective merge commit on a
`gh-readonly-queue/main/...` branch and runs the `Test` workflow on the
`merge_group` event, including the full Trino version window. The required
contexts are re-evaluated on that commit; a failing required check ejects the
pull request. Watch it with:

```bash
gh run list --workflow=test.yml --event merge_group --limit 5
RUN=$(gh run list --workflow=test.yml --event merge_group --limit 1 --json databaseId --jq '.[0].databaseId')
gh run watch "$RUN" --exit-status
```

A failed queue run ejects the pull request: fix the branch, push, and enqueue
again. Direct pushes and direct merges do not work; the ruleset rejects them.

## 3. Tag and publish

Merging the release pull request is the release trigger. The merge commit
changes `VERSION`, which starts `Create Release Tag` on `main`; a merge that
does not touch `VERSION` does nothing.

Confirm the run started on the merge commit of the release pull request:

```bash
git fetch origin main
git log --oneline -1 origin/main
gh run list --workflow=create-release-tag.yml --limit 3
```

In order, the workflow:

1. checks out the merged commit with full history,
2. reads the version from `VERSION`,
3. skips cleanly when `v<version>` already points at that commit, and fails when
   the tag exists on another commit,
4. refuses to continue when `CHANGELOG.md` has no dated section for the version,
5. imports `GPG_PRIVATE_KEY`/`PASSPHRASE` and creates an annotated, signed tag
   with the key's identity,
6. pushes the tag,
7. calls the `Release` workflow with that tag.

Step 7 is explicit because a tag pushed with the default `GITHUB_TOKEN` does not
trigger other workflows: the `push: tags: v*` trigger in `release.yml` will not
fire for this push. `Release` builds the provider, signs the checksum file,
attests provenance, generates the SBOM and replaces the GitHub release assets.

Watch the run:

```bash
gh run list --workflow=create-release-tag.yml --limit 3
RUN=$(gh run list --workflow=create-release-tag.yml --limit 1 --json databaseId --jq '.[0].databaseId')
gh run watch "$RUN" --exit-status
```

The run appears at the top of the list as soon as the push lands.

### Manual dispatch

Dispatch the tag workflow when a merge cannot be the trigger: the first tag of a
line, a retry after a failure that happened before the tag was pushed, or an
explicit version a maintainer chooses. Pass the version **without** the `v`
prefix:

```bash
gh workflow run create-release-tag.yml --ref main \
  -f version=0.2.1 \
  -f release_type=patch
```

Always pass `version`. The input is optional; when it is empty the workflow
calculates the next version from the newest tag and `release_type`. Re-running a
failed release without `version` would therefore tag a version nobody chose.
With `version` set, `release_type` is still required by the dispatch form but is
not used for the calculation. The version must already have a dated section in
`CHANGELOG.md` on `main`; the workflow refuses an undocumented release.

## 4. Verify

Start with the release itself:

```bash
gh release view v0.2.1 --repo caberdo/terraform-provider-trino \
  --json tagName,isDraft,isPrerelease,assets \
  --jq '{tagName, isDraft, isPrerelease, assets: [.assets[].name]}'
```

A healthy release is neither draft nor prerelease, and has eleven assets:

- six archives: `terraform-provider-trino_0.2.1_{darwin,linux,windows}_{amd64,arm64}.zip`
- `terraform-provider-trino_0.2.1_SHA256SUMS` and its detached `.sig`
- `terraform-provider-trino_0.2.1_manifest.json`
- `terraform-provider-trino_0.2.1_provenance.intoto.jsonl`
- `sbom.spdx.json`

Verify the checksums and the detached signature:

```bash
WORK=$(mktemp -d)
cd "$WORK"
gh release download v0.2.1 --repo caberdo/terraform-provider-trino \
  --pattern 'terraform-provider-trino_0.2.1_*'
gpg --verify terraform-provider-trino_0.2.1_SHA256SUMS.sig \
  terraform-provider-trino_0.2.1_SHA256SUMS
shasum -a 256 -c terraform-provider-trino_0.2.1_SHA256SUMS   # macOS
# sha256sum -c terraform-provider-trino_0.2.1_SHA256SUMS      # Linux
```

`shasum`/`sha256sum` must report every archive and the manifest as `OK`. If
`gpg` reports `Can't check signature: No public key`, import the signing key
first:

```bash
gh api users/dennismdejong/gpg_keys --jq '.[].raw_key' | gpg --import
```

Verify the signed tag through the API. The tag must resolve to a tag object and
the signature must be valid:

```bash
TAG_OBJECT=$(gh api repos/caberdo/terraform-provider-trino/git/ref/tags/v0.2.1 --jq .object.sha)
gh api "repos/caberdo/terraform-provider-trino/git/tags/$TAG_OBJECT" \
  --jq '.verification | {verified, reason}'
```

Expect `{"verified":true,"reason":"valid"}`. `git verify-tag v0.2.1` after
`git fetch --tags` is the same check locally.

Verify the Registry manifest and the build provenance:

```bash
curl -sL https://github.com/caberdo/terraform-provider-trino/releases/download/v0.2.1/terraform-provider-trino_0.2.1_manifest.json \
  | jq .metadata.protocol_versions
gh attestation verify terraform-provider-trino_0.2.1_linux_amd64.zip \
  --repo caberdo/terraform-provider-trino
```

The manifest must print `["6.0"]`; the Registry cannot publish a version without
it. Run the attestation check in `$WORK`; it exits 0 when the archive was built
by the release workflow.

The SBOM is a separate release asset:

```bash
gh release download v0.2.1 --repo caberdo/terraform-provider-trino --pattern sbom.spdx.json
jq -r '.spdxVersion, (.packages | length)' sbom.spdx.json
```

Finally, confirm the Registry ingested the release:

```bash
curl -s https://registry.terraform.io/v1/providers/caberdo/trino | jq '{version, description}'
curl -s https://registry.terraform.io/v1/providers/caberdo/trino/versions \
  | jq '.versions[] | {version, protocols}'
```

The Registry syncs GitHub releases on its own schedule. Immediately after
publishing, the top-level `version` can still be the previous release; wait for
the new version to appear instead of re-dispatching the release to nudge it.
Once it is listed, a clean install must work end to end:

```bash
DIR=$(mktemp -d) && cd "$DIR"
cat > main.tf <<'EOF'
terraform {
  required_providers {
    trino = {
      source  = "caberdo/trino"
      version = "0.2.1"
    }
  }
}
EOF
terraform init
```

`terraform init` must install `caberdo/trino v0.2.1` and report the signing key
ID.

## 5. Recover

If the tag was pushed but the release failed or published incomplete assets,
dispatch `Release` with the existing tag:

```bash
gh workflow run release.yml --ref main -f tag=v0.2.1
```

`release.yml` checks out that tag, and GoReleaser runs with
`release.mode: replace`, so republishing is idempotent: assets are replaced and
the signed tag is untouched.

Do not re-run `Create Release Tag` to fix a publish failure. It skips a tag that
already points at the commit and cannot replace a release; with `version` empty
it would also calculate a version nobody intended. `Release` with an explicit
`tag` is the recovery tool.

If `Create Release Tag` failed *before* pushing the tag (GPG import, signing),
fix the cause and dispatch it again with the same explicit `version`.

If a published release is broken, do not delete or move a tag the Registry has
seen. Ship a patch.

## 6. Patch or minor

| | Patch (`0.2.x`) | Minor (`0.x.0`) |
|---|---|---|
| Use for | bug fixes, docs, CI and dependency changes; state-compatible | new resources, attributes or auth modes, or changed behaviour |
| Compatibility | safe drop-in | pre-1.0, a minor may break compatibility; call it out in the changelog |
| README | leave the install snippet and status line alone | update the install snippet to the new minor and the status line |
| Changelog | entries under `### Fixed` / `### Changed` | as for a patch, plus `### Added` for new surface |

Terraform's pessimistic constraint matters here: `~> 0.2` means
`>= 0.2.0, < 1.0.0`, so it accepts `0.3.0` and later minors. If a consumer wants
patch-only updates they must write `~> 0.2.0` (`>= 0.2.0, < 0.3.0`). At a minor
release, bump the README snippet to the new minor so fresh installs and new
readers see the current line.

Only the latest release receives security fixes; see [`SECURITY.md`](../SECURITY.md).

# Releasing the provider

This is the maintainer runbook for shipping a release. It reproduces the path
that shipped `0.1.1` end to end: merge through the queue, create the signed tag
from a workflow, publish with GoReleaser, verify the artifacts, and recover when
a step fails. The release is deliberately manual.

The example version in every command is `0.1.2`. Substitute the version you are
shipping; the tag always carries a `v` prefix. Run the commands from a clone of
the repository with `gh` authenticated as a maintainer.

You need write access to the repository. The repository also needs the two
secrets the release workflows use: `GPG_PRIVATE_KEY` and `PASSPHRASE`. The
signing key must be registered to the account that owns the tagger email,
otherwise GitHub shows the tag as unverified even though the signature is
valid.

## 1. Pre-flight

Start from a clean `main`:

```bash
git switch main
git pull --ff-only
```

Confirm the target tag does not exist. The first command must print nothing and
the second must exit non-zero:

```bash
git ls-remote --tags origin "refs/tags/v0.1.2"
git rev-parse --verify "refs/tags/v0.1.2"
```

Update `CHANGELOG.md`: move everything under `## Unreleased` into a new
`## [0.1.2] - YYYY-MM-DD` section, leaving the `## Unreleased` heading in place.

Run the release gates locally and prove the generated reference is clean:

```bash
make build vet lint
make coverage
make coverage-gate
make coverage-readme-check
make validate-examples
make generate
git diff --exit-code
```

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

Confirm the tip of `main` is the merge commit of the release pull request:

```bash
git fetch origin main
git log --oneline -1 origin/main
```

Dispatch the tag workflow, passing the version **without** the `v` prefix:

```bash
gh workflow run create-release-tag.yml --ref main \
  -f version=0.1.2 \
  -f release_type=patch
```

Always pass `version`. The input is optional; when it is empty the workflow
calculates the next version from the newest tag and `release_type`. Re-running a
failed release without `version` would therefore tag a version nobody chose.
With `version` set, `release_type` is still required by the dispatch form but is
not used for the calculation.

In order, the workflow:

1. checks out `main` with full history,
2. refuses to continue if the tag already exists,
3. imports `GPG_PRIVATE_KEY`/`PASSPHRASE` and creates an annotated, signed tag
   with the key's identity,
4. pushes the tag,
5. calls the `Release` workflow with that tag.

Step 5 is explicit because a tag pushed with the default `GITHUB_TOKEN` does not
trigger other workflows: the `push: tags: v*` trigger in `release.yml` will not
fire for this push. `Release` builds the provider, signs the checksum file,
attests provenance, generates the SBOM and replaces the GitHub release assets.

Watch the workflow run:

```bash
gh run list --workflow=create-release-tag.yml --limit 3
RUN=$(gh run list --workflow=create-release-tag.yml --limit 1 --json databaseId --jq '.[0].databaseId')
gh run watch "$RUN" --exit-status
```

The run appears at the top of the list immediately after dispatch.

## 4. Verify

Start with the release itself:

```bash
gh release view v0.1.2 --repo caberdo/terraform-provider-trino \
  --json tagName,isDraft,isPrerelease,assets \
  --jq '{tagName, isDraft, isPrerelease, assets: [.assets[].name]}'
```

A healthy release is neither draft nor prerelease, and has eleven assets:

- six archives: `terraform-provider-trino_0.1.2_{darwin,linux,windows}_{amd64,arm64}.zip`
- `terraform-provider-trino_0.1.2_SHA256SUMS` and its detached `.sig`
- `terraform-provider-trino_0.1.2_manifest.json`
- `terraform-provider-trino_0.1.2_provenance.intoto.jsonl`
- `sbom.spdx.json`

Verify the checksums and the detached signature:

```bash
WORK=$(mktemp -d)
cd "$WORK"
gh release download v0.1.2 --repo caberdo/terraform-provider-trino \
  --pattern 'terraform-provider-trino_0.1.2_*'
gpg --verify terraform-provider-trino_0.1.2_SHA256SUMS.sig \
  terraform-provider-trino_0.1.2_SHA256SUMS
shasum -a 256 -c terraform-provider-trino_0.1.2_SHA256SUMS   # macOS
# sha256sum -c terraform-provider-trino_0.1.2_SHA256SUMS      # Linux
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
TAG_OBJECT=$(gh api repos/caberdo/terraform-provider-trino/git/ref/tags/v0.1.2 --jq .object.sha)
gh api "repos/caberdo/terraform-provider-trino/git/tags/$TAG_OBJECT" \
  --jq '.verification | {verified, reason}'
```

Expect `{"verified":true,"reason":"valid"}`. `git verify-tag v0.1.2` after
`git fetch --tags` is the same check locally.

Verify the Registry manifest and the build provenance:

```bash
curl -sL https://github.com/caberdo/terraform-provider-trino/releases/download/v0.1.2/terraform-provider-trino_0.1.2_manifest.json \
  | jq .metadata.protocol_versions
gh attestation verify terraform-provider-trino_0.1.2_linux_amd64.zip \
  --repo caberdo/terraform-provider-trino
```

The manifest must print `["6.0"]`; the Registry cannot publish a version without
it. Run the attestation check in `$WORK`; it exits 0 when the archive was built
by the release workflow.

The SBOM is a separate release asset:

```bash
gh release download v0.1.2 --repo caberdo/terraform-provider-trino --pattern sbom.spdx.json
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
      version = "0.1.2"
    }
  }
}
EOF
terraform init
```

`terraform init` must install `caberdo/trino v0.1.2` and report the signing key
ID.

## 5. Recover

If the tag was pushed but the release failed or published incomplete assets,
dispatch `Release` with the existing tag:

```bash
gh workflow run release.yml --ref main -f tag=v0.1.2
```

`release.yml` checks out that tag, and GoReleaser runs with
`release.mode: replace`, so republishing is idempotent: assets are replaced and
the signed tag is untouched.

Never re-run `Create Release Tag` to fix a publish failure. It refuses an
existing tag, and with `version` empty it would calculate a new version nobody
intended. `Release` with an explicit `tag` is the recovery tool.

If `Create Release Tag` failed *before* pushing the tag (GPG import, signing),
fix the cause and dispatch it again with the same explicit `version`.

If a published release is broken, do not delete or move a tag the Registry has
seen. Ship a patch.

## 6. Patch or minor

| | Patch (`0.1.x`) | Minor (`0.x.0`) |
|---|---|---|
| Use for | bug fixes, docs, CI and dependency changes; state-compatible | new resources, attributes or auth modes, or changed behaviour |
| Compatibility | safe drop-in | pre-1.0, a minor may break compatibility; call it out in the changelog |
| README | leave the install snippet and status line alone | update the install snippet to the new minor and the status line |
| Changelog | entries under `### Fixed` / `### Changed` | as for a patch, plus `### Added` for new surface |

Terraform's pessimistic constraint matters here: `~> 0.1` means
`>= 0.1.0, < 1.0.0`, so it accepts `0.2.0` and later minors. If a consumer wants
patch-only updates they must write `~> 0.1.0` (`>= 0.1.0, < 0.2.0`). At a minor
release, bump the README snippet to the new minor so fresh installs and new
readers see the current line.

Only the latest release receives security fixes; see [`SECURITY.md`](../SECURITY.md).

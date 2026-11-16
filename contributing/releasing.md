# Releasing the provider

This is the maintainer runbook for shipping a release. A release is a signed
`v*` tag cut after the release pull request merges: the newest `CHANGELOG.md`
section names the version, and this repository carries no `VERSION` file. The
release pull request assembles `CHANGELOG.md` from the fragments in
`changelog.d/` and, at a minor release, updates the README install
constraint; merging it publishes nothing. Pushing the signed tag is what starts
the `Release` workflow, which publishes the release with GoReleaser. This
runbook covers that path end to end, plus the manual dispatch and recovery steps
for the cases a tag push cannot cover.

The example version in every command is `0.2.1`. Substitute the version you are
shipping; the tag always carries a `v` prefix. Run the commands from a clone of
the repository with `gh` authenticated as a maintainer.

You need write access to the repository. The repository also needs the two
secrets the release workflow uses: `GPG_PRIVATE_KEY` and `PASSPHRASE`. The
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
release pull request changes two files consistently, plus any regenerated
reference under `docs/`:

1. `CHANGELOG.md` — group the `changelog.d/` fragments by kind under a new
   `## [0.2.1] - YYYY-MM-DD` section, delete the processed fragments, and leave
   the `## Unreleased` heading in place. This is the only pull request that
   edits the changelog; there is no `VERSION` file to bump.
2. `README.md` — the status line and, at a minor release, the install
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
make validate-examples
make generate
git diff --exit-code
```

`make release-check` derives the version from the newest `CHANGELOG.md` section
and compares it with the README install constraint. Before the tag exists it
reports `pending release`; after step 3 it reports `released`. It exits 0
whenever the changelog and the README agree, so run it here and again after the
tag.

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

## 3. Cut the tag and publish

Merging the release pull request is not a release: the changelog names the
version, but no workflow cuts a tag on a merge. Cut a signed tag on the merge
commit you intend to ship and push it; that push is the release trigger.

```bash
git switch main
git pull --ff-only
git tag -s v0.2.1 -m "Release v0.2.1"
git tag -v v0.2.1
git push origin v0.2.1
```

The signing key must be the one registered to the account that owns the tagger
email; the same key is held in the `GPG_PRIVATE_KEY`/`PASSPHRASE` secrets for the
checksum signature.

In order, the `Release` workflow:

1. checks out the tagged commit with full history,
2. imports `GPG_PRIVATE_KEY`/`PASSPHRASE`,
3. builds the provider, signs the checksum file, attests provenance, generates
   the SBOM and publishes the GitHub release for the tag.

The kit's tag-creation job does not run without a version file, so no workflow
invents a version: the tag you push is exactly what ships. GoReleaser runs with
`release.mode: replace`, so a rerun of an existing tag replaces its assets
without moving the tag.

Watch the run:

```bash
gh run list --workflow=release.yml --limit 3
RUN=$(gh run list --workflow=release.yml --limit 1 --json databaseId --jq '.[0].databaseId')
gh run watch "$RUN" --exit-status
```

### Manual dispatch

Dispatch `Release` with an existing tag to publish or republish it. This covers
a tag pushed without a workflow run being started, and a publish that failed
after the tag existed:

```bash
gh workflow run release.yml --ref main -f tag=v0.2.1
```

The tag must already exist. A dispatch cannot choose a new version, because
nothing but a tag names one.

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

`Release` checks out that tag, and GoReleaser runs with `release.mode: replace`,
so republishing is idempotent: assets are replaced and the signed tag is
untouched.

Do not delete or move the tag to recover a publish failure. If the tag push
never started a run, dispatch `Release` with the tag instead; if the tag itself
was wrong, ship a new patch tag.

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

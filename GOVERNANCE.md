# Governance

How decisions are made in `caberdo/terraform-provider-trino`, who is
responsible for what, and what happens if a maintainer steps away. Kept deliberately short and
factual: this is a single-repository, effectively single-maintainer project today, and the document
says so instead of describing a committee that does not exist. Where a setting is missing, that is
stated as an open item rather than implied.

## Roles

| Role | Who | Responsibilities |
|---|---|---|
| **Maintainer** | [@dennismdejong](https://github.com/dennismdejong) | Final say on scope and design; review and merge pull requests; cut releases (tag + GoReleaser run); triage issues; handle private vulnerability reports; hold the release signing key and the repository's Actions secrets |
| **Organization owner** | the [`caberdo`](https://github.com/caberdo) GitHub organization | Owns the repository and the Terraform Registry namespace `caberdo/trino`, so ownership does not disappear with a personal account |
| **Contributor** | anyone | Propose changes through pull requests; file reproducible bug reports; take part in issue discussion. Requirements are in [`CONTRIBUTING.md`](CONTRIBUTING.md) |
| **Security reporter** | anyone | Report vulnerabilities privately as described in [`SECURITY.md`](SECURITY.md); reporters are credited in the release notes unless they ask otherwise |

There is no separate release manager, documentation owner or triage team: the maintainer holds those
responsibilities and the workflows in `.github/workflows/` do the mechanical parts.

## How decisions are made

1. **Anything non-trivial starts as an issue.** Bugs and features arrive through the issue forms in
   `.github/ISSUE_TEMPLATE/`. Design questions are decided *in* the issue, with the alternatives and
   the reasoning written down, so a later reader can see why the code looks the way it does.
2. **Every change lands through a pull request against `main`.** The default branch's ruleset
   forbids deletion and force-pushes; the other rules it enforces are recorded in the honest-state
   note below.
3. **The maintainer decides.** Where an issue reaches no consensus, the maintainer records the
   decision and its rationale in the issue and moves on. Disagreement is expressed in the issue,
   not by re-litigating a merged change.
4. **Releases are cut, not continuous.** The reusable release workflow computes the next version,
   signs and pushes the tag, builds with GoReleaser, signs the checksums, attests build provenance,
   attaches the SPDX SBOM and publishes to the Terraform Registry; the public verification steps are
   in [`README.md`](README.md#verifying-a-release). A failed release is republished by dispatching
   the release workflow with the existing tag. `CHANGELOG.md` is updated per release; breaking
   changes are called out in it.
5. **Backwards compatibility is a commitment.** Terraform type and attribute names are public API:
   renaming a type or an attribute silently breaks user state. An attribute the API cannot change in
   place carries `RequiresReplace()` instead of silently doing nothing, and Go package moves never
   change a Terraform type name.

## Continuous integration as the gate

The provider is a short caller of the reusable workflows in
`caberdo/terraform-provider-kit`; the kit owns the steps and the provider owns the triggers. The
canonical checks, where a provider enables them, are:

| Check | What it verifies | Blocks what |
|---|---|---|
| `ci / Lint & Security` — golangci-lint (pinned) and gosec (SARIF → code scanning, job fails on findings) | static analysis of the change | a red run is treated as blocking; see the note below |
| `ci / Test` — build, `go vet`, `go mod tidy`/`verify`, cross-platform build, example validation, generated-docs drift, `go test -race` with coverage | the unit suite and the generated artefacts | same |
| `ci / Coverage` — the merged unit + acceptance profile checked against `.github/coverage-baseline.txt` by `tfkit coverage gate`, plus the README/baseline drift check | the coverage floor and the README block | a change that drops a scope below its floor, or a README coverage block that no longer matches the baseline |
| `ci / Vulnerability Scan` — `govulncheck` | reachable dependency vulnerabilities | a pull request that introduces one |
| `ci / Dependency Review` | vulnerable dependency introduced by the pull request | same |
| `ci / Generate Docs` (opens a PR on drift) / `ci / Validate Markdown Files` | merged documentation matches the schema, and links resolve | merged documentation that no longer matches the schema, and broken links |
| `acceptance / Acceptance` — the live suite against the supported server matrix | resource and data source behaviour against a real server | a release from a `main` whose acceptance matrix is red |
| Release — GoReleaser from a signed tag, signed checksums, SLSA build provenance, SPDX SBOM | the release artifacts | a release whose artifacts are unsigned or un-attested |

**Honest state of enforcement.** Whether the default branch's ruleset requires the checks above and
a code-owner review is a repository setting; where it does not, CI still reports on every change,
but a merge is not mechanically blocked by a red run. That is the maintainer's commitment, not an
enforced rule, and the gap belongs in the list below rather than in an implicit assumption.

Two related gaps are recorded here rather than papered over:

* Required status checks should be added to the ruleset: `ci / Lint & Security` and `ci / Test` at
  minimum, plus the checks the repository enables. Until then the gate is trusted rather than
  enforced.
* `.github/CODEOWNERS` names the *organization*, while GitHub resolves code owners to users or teams
  with write access. A ruleset's code-owner requirement therefore has no effective owner while the
  project has one maintainer. Naming the maintainer directly is not a free fix: GitHub does not let
  an author satisfy a code-owner review, so it would block the maintainer's own pull requests until
  there is a second person with write access. The decision is deliberately left to the maintainer.

## Access and continuity

The project must be able to keep working if one person becomes unavailable. What exists today:

* The repository, the registry namespace and the release identity belong to the
  `caberdo` organization rather than a personal account.
* The release key pair and its passphrase live in the repository's encrypted Actions secrets
  (`GPG_PRIVATE_KEY`, `PASSPHRASE`), used only by the release workflows, and the public half is
  published on the maintainer's GitHub account, so any user can verify a release without asking for
  a key (`README.md#verifying-a-release` gives the `gpg --import` step); every release is
  reproducible from a tag by anyone with repository and secret access.
* The whole release procedure is the reusable workflows in the kit, and the architecture behind it
  is written down in [`ARCHITECTURE.md`](ARCHITECTURE.md).

**Open gap — bus factor.** There is one maintainer and the organization has one member. To satisfy
the continuity requirement — *the project can still triage issues, accept changes and release within
a week if any one person is lost* — a second person needs (1) `maintain` or `admin` on the
organization, (2) membership of the secret-holding team, and (3) a documented hand-over in this
file. Until then, continuity depends on the organization remaining reachable by its current owner.

Two organization settings that belong to the same gap and may be **off** (organisation settings →
Authentication security / Code security): two-factor authentication is not required for members,
and secret scanning with push protection may be disabled. Turning both on is an owner task, and
neither needs a code change.

## Adding maintainers and reviewers

1. The candidate contributes at least one reviewed change and shows they can run the suites
   (`make testacc` and `make testacc-docker`).
2. An existing maintainer invites them as a collaborator and grants organization `maintain`.
3. When they are expected to review: fix `.github/CODEOWNERS` to name them (or a team) and set the
   approval requirement plus the required status checks on the default-branch ruleset. The decision
   and its date are recorded in the issue that proposed it.
4. Their name is added to the roles table above.

## Changing this document

Governance changes are proposed by pull request like any other change. If a second maintainer
joins, the roles table and the continuity section are updated in the same pull request that grants
the access, and the open items in this file are closed or restated.

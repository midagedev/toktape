# Releasing toktape

Maintainer notes. A release is a pushed `v*` tag; everything else is
automatic.

## Once, before the first tag

- Create the tap repository `midagedev/homebrew-tap` (it exists).
- Add the repository secret `HOMEBREW_TAP_GITHUB_TOKEN`: a token with write
  access to `midagedev/homebrew-tap`. The built-in `GITHUB_TOKEN` cannot write
  to another repository, so without this secret the release still publishes
  but the formula is not updated.

## Every release

```sh
./scripts/check.sh                       # green on main
git tag v0.1.0
git push origin v0.1.0
```

Before the tag, two things the workflow cannot do for you:

- **Rewrite `release.header` in `.goreleaser.yaml`.** The changelog is every
  commit subject in one flat list; the header is what a reader has to know
  before reading it, and it is written per release, not templated. Check
  each claim against its source (the commit, the line of code) before it
  goes in. Commit it, push, and let CI go green on that push.
- **Look at the previous push's CI, not only the local gate.** v0.4.0's
  hero re-pin was red on CI alone (a viewer-timezone date); the local gate
  was green.

Minor when the tape schema or the caveat set grew, patch otherwise.

`.github/workflows/release.yml` runs GoReleaser (`.goreleaser.yaml`):

- static binaries for linux/darwin/windows × amd64/arm64, `-X main.version=<tag>`;
- archives named `toktape_<version>_<os>_<arch>.tar.gz` plus a plain
  `checksums.txt` — `scripts/install.sh` reconstructs exactly that name from
  `uname`, and the `release-contract` CI job asserts the two agree on every
  push;
- a GitHub release with the changelog grouped from commit subjects and the
  install block in the footer;
- the Homebrew formula pushed to `midagedev/homebrew-tap`.

Afterwards, check the three install paths on a clean machine:

```sh
brew install midagedev/tap/toktape
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
go install github.com/midagedev/toktape/cmd/toktape@latest
toktape version
```

## If the release job fails

Delete the tag locally and remotely, fix, and tag again with the same
version only if nothing was published. If assets were published, bump the
patch version instead; `install.sh` users pin by tag and a moved tag breaks
their checksums.

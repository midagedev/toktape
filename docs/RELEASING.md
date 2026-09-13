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

`.github/workflows/release.yml` runs GoReleaser (`.goreleaser.yaml`):

- static binaries for linux/darwin × amd64/arm64, `-X main.version=<tag>`;
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

# Orb

Orb is a CLI-native, open-source agentic coding interface built in Go with Bubble Tea.

## Install (macOS, one command)

```bash
brew install --HEAD willsantiagomedina/orb/orb
```

## Upgrade

```bash
brew upgrade --fetch-HEAD willsantiagomedina/orb/orb
```

## Run

```bash
orb
```

## Homebrew Tap Notes

Orb ships a tap formula in this repo at `Formula/orb.rb`.

- Tap namespace: `willsantiagomedina/orb`
- Formula name: `orb`
- One-command install path: `brew install --HEAD willsantiagomedina/orb/orb`

## Releases

A GitHub Actions release workflow is configured at `.github/workflows/release.yml`.

- Trigger: push a tag matching `v*` (for example `v0.1.0`)
- Outputs: macOS archives for `darwin/amd64` and `darwin/arm64`, plus `checksums.txt`
- Publish: creates a GitHub Release with generated notes and uploaded artifacts

Tag and release example:

```bash
git tag v0.1.0
git push origin v0.1.0
```

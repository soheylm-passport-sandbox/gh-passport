# gh-passport

`gh-passport` is the public, cross-platform GitHub CLI extension for the IDEAL
Lab IT and Research Computing Passport.

## Install

First install [GitHub CLI](https://cli.github.com/) and authenticate:

```text
gh auth login --web --git-protocol https
gh extension install soheylm-passport-sandbox/gh-passport --force --pin v0.4.0
```

Then start the local setup wizard:

```text
gh passport start
```

No public record is created until you choose your work, read the public-content
notice, and confirm it in the browser. The launcher then creates or safely
reuses its managed files and opens your first mission. It never deletes an
unrelated folder, force-pushes, merges a PR, or rewrites an existing route.

Later, run these from any folder:

```text
gh passport open
gh passport status
gh passport sync
gh passport doctor
```

Local browser state remembers navigation and drafts only. The generated public
learning record holds sanitized submissions. The trusted lab controller is the
only source accepted for completion status. Git transport remains in the
background until the Git mission teaches it explicitly.

## Public Evidence Safety

The learning record is public. Your GitHub username is necessarily visible.
Submit only the fictional and sanitized values requested by the exercise.
Never add credentials, ETH or other private identifiers, local paths, job IDs,
private logs, screenshots, AI transcripts, research data, or confidential
project details.

## Supported Releases

Release assets follow GitHub CLI's documented naming convention for Windows
amd64, macOS amd64/arm64, and Linux amd64/arm64. Source installs are for
maintainer development only.

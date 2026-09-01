# gh-passport

`gh-passport` is the public, cross-platform GitHub CLI extension for the IDEAL
Lab IT and Research Computing Passport.

## Install

First install [GitHub CLI](https://cli.github.com/) and authenticate:

```text
gh auth login --web --git-protocol https
gh extension install soheylm-passport-sandbox/gh-passport
```

Then start or resume the complete local-first journey:

```text
gh passport start
```

The command creates or reuses your personal public fork of
`soheylm-passport-sandbox/passport-exercises`, creates one permanent assessment branch and
draft pull request, then opens the local browser interface. It never deletes a
folder, force-pushes, merges a PR, or rewrites an existing route.

From the local exercise folder, use:

```text
gh passport open
gh passport status
gh passport sync
gh passport doctor
```

Local browser state remembers navigation only. Git commits and the public
assessment PR hold submitted exercise evidence. The private lab controller is
the only source accepted for automatic completion status.

## Public Evidence Safety

The assessment PR is public. Your GitHub username is necessarily visible
through the fork. Submit only the fictional and sanitized values requested by
the exercise. Never add credentials, ETH or other private identifiers, private
logs, screenshots with private information, research data, or confidential
project details.

## Supported Releases

Release assets follow GitHub CLI's documented naming convention for Windows
amd64, macOS amd64/arm64, and Linux amd64/arm64. Source installs are for
maintainer development only.

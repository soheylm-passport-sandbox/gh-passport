# Frozen public browser qualification

This tests the exact public launcher pages, not a checkout of the private source.
The payload hashes identify the qualified source and every static asset. The
browser test files are copied unchanged from that source. Inputs are fictional
learner contexts and lesson metadata already published in the public exercise
catalogue; the agent rules already ship in the public launcher. No student
records, private source checkout, deployment credential or private API is used.

All WebKit cases remain required. This complements the exact-source private
Python/Go/native and Chromium/Firefox gates. Only standard public runners with
read-only repository permissions are used.

The disposable macOS runner uses one browser worker and disables only native
window animations in the test application domain. No student browser, personal
Mac setting, CSS animation, sandbox, permission or test assertion is changed.

The native-window preference is written only into an owned temporary app-data
folder, passed to WebKit through `CFFIXED_USER_HOME`, and removed in global
teardown. It does not modify the runner user's global preferences.

When exporting a later curriculum, copy its public pages and unchanged synthetic
tests again, refresh `payload.json`, and compare them with the exact source build.
The verifier intentionally rejects stale assets. Keep the two mission fixtures
limited to the already-public exercise catalogue and the agent contract identical
to the public launcher contract. Never copy private records or deploy credentials.

The native macOS test retains the existing protected `test (macos-latest)` check
name while explicitly selecting the standard `macos-26-intel` runner. The test
command is unchanged; no branch protection rule is relaxed.

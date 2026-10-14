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

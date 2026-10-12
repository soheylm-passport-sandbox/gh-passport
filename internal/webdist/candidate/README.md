# Local Handover Bundle

Version 6.0.0 is an explicit local integration proposal, not the registration
or release default. The generated `6.0.0/` directory is ignored by Git.

The maintainer integration preview supports `--embed` to stage native pages
for a development binary. This operation never publishes.

Then build the Go launcher normally. An unstaged candidate fails closed;
it cannot silently use another version's pages. Existing 2.1.2, 3.0.0,
4.0.0 and 5.0.0 bundles keep their own contracts.

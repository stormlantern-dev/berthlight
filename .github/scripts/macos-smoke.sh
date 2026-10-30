#!/usr/bin/env bash
# Checks, on a Mac, that a berthlight binary is signed with the Developer
# ID, satisfies its designated requirement, is notarized, and runs (#58).
#
#   .github/scripts/macos-smoke.sh <path to berthlight> [version]
set -euo pipefail

bin=${1:?usage: macos-smoke.sh <path to berthlight> [version]}
want=${2:-}

# Fails on "does not satisfy its designated Requirement", which is what
# got v0.8.0 killed on launch.
codesign --verify --strict --verbose=2 "$bin"
info=$(codesign -dv --verbose=4 "$bin" 2>&1)
grep -q '^Authority=Developer ID Application' <<<"$info" ||
	{ echo "::error::not signed with a Developer ID Application certificate"; exit 1; }
grep -q 'flags=.*runtime' <<<"$info" ||
	{ echo "::error::the hardened runtime is missing"; exit 1; }
# Gatekeeper's verdict, asked of Apple online: a bare binary carries no
# stapled ticket.
verdict=$(spctl -a -vv -t install "$bin" 2>&1 || true)
echo "$verdict"
grep -q 'source=Notarized Developer ID' <<<"$verdict" ||
	{ echo "::error::Gatekeeper does not see a notarized Developer ID binary"; exit 1; }
out=$("$bin" version)
echo "$out"
if [ -n "$want" ] && ! grep -qF "$want" <<<"$out"; then
	echo "::error::berthlight version printed '$out', not $want"
	exit 1
fi

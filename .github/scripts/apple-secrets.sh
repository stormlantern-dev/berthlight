#!/usr/bin/env bash
# Checks the Apple signing secrets in the environment (#58): all five are
# set, and the .p12 opens and holds a chain quill signs correctly with.
set -euo pipefail

missing=0
for v in MACOS_DEVELOPER_ID_P12 MACOS_DEVELOPER_ID_P12_PASSWORD NOTARY_API_KEY_P8 NOTARY_API_KEY_ID NOTARY_API_ISSUER_ID; do
	if [ -z "${!v:-}" ]; then
		echo "::error::secret $v is missing from the release environment (#58)"
		missing=1
	fi
done
if [ "$missing" = 1 ]; then exit 1; fi

# The .p12 must hold the leaf certificate alone (as Keychain exports it),
# or the full chain up to Apple's root. With only the intermediate beside
# the leaf, quill takes the intermediate for the root and writes a
# designated requirement macOS 26 does not accept: the binary is killed on
# launch (v0.8.0, #58).
pem=$(base64 --decode <<<"$MACOS_DEVELOPER_ID_P12" |
	openssl pkcs12 -nokeys -passin env:MACOS_DEVELOPER_ID_P12_PASSWORD 2>/dev/null || true)
certs=$(grep -c 'BEGIN CERTIFICATE' <<<"$pem" || true)
root=$(grep -c 'subject=.*CN *= *Apple Root CA' <<<"$pem" || true)
if [ "$certs" = 0 ]; then
	echo "::error::MACOS_DEVELOPER_ID_P12 cannot be opened with its password (#58)"
	exit 1
fi
if [ "$certs" != 1 ] && [ "$root" = 0 ]; then
	echo "::error::MACOS_DEVELOPER_ID_P12 holds $certs certificates without Apple's root: export the leaf certificate and key only (#58)"
	exit 1
fi
echo "Apple signing secrets: all set; the .p12 holds $certs certificate(s)"

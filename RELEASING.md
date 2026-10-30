# Releasing berthlight

## Cutting a release

Tag a commit on `main` and push the tag:

```nu
git tag -a v0.9.0 -m "berthlight 0.9.0"
git push origin v0.9.0
```

The `Release` workflow (`.github/workflows/release.yml`) does the rest. In
order:
1. **`macos-sign` and `macos-smoke`:** build the darwin arm64 binary, signed
   and notarized, and check on a Mac that it runs (see below). If this
   fails, nothing is published.
2. **`release`:** GoReleaser publishes the archives, the packages, the
   Homebrew cask, the Scoop manifest, the AUR package and the proxy image,
   with signed checksums and build provenance.
3. **The install jobs:** install the release the way users do, with `brew`
   (macOS and Linux), `scoop`, `dpkg` and the image, and run it.

Afterwards, check:
- the checksums;
- `gh attestation verify` on the archives;
- that `proxy.golang.org` lists the version.

## Credentials

The tap, bucket and AUR keys live in Cloud KMS and are reached through
Workload Identity Federation. Nothing about them is stored in GitHub.
`scripts/gcp-release.nu` sets that up, and the `Verify release credentials`
workflow tests it.

The Apple credentials can't be used from KMS. They are secrets of the
`release` environment, which only tags `v*` may deploy to. They are
berthlight's own, not shared with other projects:

| Secret | What |
|---|---|
| `MACOS_DEVELOPER_ID_P12` | base64 of the Developer ID Application certificate and its key, as `.p12` |
| `MACOS_DEVELOPER_ID_P12_PASSWORD` | the `.p12`'s password |
| `NOTARY_API_KEY_P8` | base64 of the App Store Connect API key "berthlight notary" (`.p8`) |
| `NOTARY_API_KEY_ID` | that key's ID |
| `NOTARY_API_ISSUER_ID` | the team's issuer ID |

The operator's password manager keeps the `.p12` and its password. The
`.p8` isn't kept: if it's lost, revoke the key and make a new one.

### macOS signing

GoReleaser signs the darwin binaries with the Developer ID (hardened
runtime) and notarizes them, on Linux, with quill (`notarize.macos` in
`.goreleaser.yaml`). A bare binary carries no stapled ticket, so Gatekeeper
looks the notarization up online. The cask needs no quarantine workaround.

**The `.p12` must hold the leaf certificate alone**, as Keychain Access
exports it, or the full chain up to Apple's root. Both `macos-sign` and
`release` refuse anything else (`.github/scripts/apple-secrets.sh`).

With the leaf and Apple's intermediate but no root, quill treats the
intermediate as the root. It then writes a designated requirement the
binary doesn't satisfy, and macOS kills it on launch. That is what broke
v0.8.0 (#58).

### Renewing the certificate

The Developer ID Application certificate expires on **2027-11-29**.
Binaries signed before then keep working, because they carry a secure
timestamp. Releases after it fail at `macos-sign`.

A new certificate can be made without a Mac:

1. Make a key and a certificate signing request (CSR):

   ```nu
   openssl genrsa -out berthlight-devid.key 2048
   openssl req -new -key berthlight-devid.key -out berthlight-devid.csr -subj "/emailAddress=dirk.louwers@stormlantern.nl/CN=berthlight Developer ID/C=NL"
   ```

2. Upload the CSR at
   [developer.apple.com](https://developer.apple.com/account/resources/certificates/add):
   - choose *Developer ID Application*;
   - choose *G2 Sub-CA*;
   - upload `berthlight-devid.csr`;
   - download `developerID_application.cer`.

3. Make the `.p12` with the leaf only, so no `-certfile`. The password comes
   from your password manager:

   ```nu
   $env.P12PW = (input -s "p12 password: ")
   openssl x509 -inform der -in developerID_application.cer -out berthlight-devid.pem
   openssl pkcs12 -export -inkey berthlight-devid.key -in berthlight-devid.pem -name "berthlight Developer ID" -certpbe PBE-SHA1-3DES -keypbe PBE-SHA1-3DES -macalg sha1 -passout env:P12PW -out berthlight-devid.p12
   ```

4. Set the secrets:

   ```nu
   open --raw berthlight-devid.p12 | encode base64 | gh secret set MACOS_DEVELOPER_ID_P12 --env release -R stormlantern-dev/berthlight
   $env.P12PW | gh secret set MACOS_DEVELOPER_ID_P12_PASSWORD --env release -R stormlantern-dev/berthlight
   hide-env P12PW
   ```

5. Store the `.p12` and its password in your password manager. Then delete
   the key, the CSR, the `.cer`, the `.pem` and the `.p12` from disk.

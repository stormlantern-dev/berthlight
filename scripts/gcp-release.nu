#!/usr/bin/env nu
# GCP side of berthlight's release credentials (PRD 0001, R9; issue #1), in
# the stormlantern-release project. The same pattern as
# dlouwers/experiments scripts/gcp-tap-kms.nu (#505), for this repository.
#
# Run from the repository root ON THE HOST, where gcloud is.
#
#   ./scripts/gcp-release.nu setup            # APIs, WIF pool and provider, keyring, import-only key, secret, bindings
#   ./scripts/gcp-release.nu import <pem>     # OPERATOR: import a key generated on the release App, then delete the .pem
#   ./scripts/gcp-release.nu aur-key <file>   # OPERATOR: add the AUR private key as a secret version, then delete the file
#   ./scripts/gcp-release.nu fingerprint      # GitHub-style fingerprint of the KMS public key(s)
#   ./scripts/gcp-release.nu status           # print what exists, change nothing
#
# ── What it grants ─────────────────────────────────────────────────────────
#
# GitHub does not accept federated identities: an installation token needs a
# JWT signed with the App's private key. So the key is imported into KMS
# (non-exportable) and release.yml proves who it is through GitHub OIDC →
# WIF to get permission to SIGN with it (.github/actions/app-token). The AUR
# only takes SSH keys, so that key is a Secret Manager secret the same
# principal may read (.github/actions/aur-key). Nothing else: no service
# account, no project-wide role.
#
# The provider admits repository 1430923799 (stormlantern-dev/berthlight)
# by numeric id, and only release.yml on a v* tag push or
# verify-release-credentials.yml dispatched on main (job_workflow_ref).
#
# ── Rotation ───────────────────────────────────────────────────────────────
#
# App key: generate a new key on the App, `import` it (a new key VERSION),
# point TAP_KMS_KEY_VERSION at it, run verify-release-credentials.yml, then
# delete the old key on the App and disable the old version here.
# AUR key: add the new public key to the AUR account, `aur-key` the private
# one (AUR_KEY_SECRET_VERSION follows `latest`), verify, remove the old
# public key from the account, and disable the old secret version.

const PROJECT = "stormlantern-release"
const PROJECT_NUMBER = "369937005182"
const LOCATION = "europe-west4"
const KEYRING = "github-apps"
const KEY = "release-app"  # the tap App (id 4637258), once it belongs to stormlantern-dev
const ALGORITHM = "rsa-sign-pkcs1-2048-sha256"  # RS256, what GitHub signs and verifies App JWTs with
const SECRET = "aur-ssh-key"

const GH_POOL = "github-actions"
const GH_PROVIDER = "berthlight"
const GH_ISSUER = "https://token.actions.githubusercontent.com"
# Numeric ids: a re-created repository with the same name gets a new id and
# is NOT trusted. Read 2026-10-23 from `gh api repos/stormlantern-dev/berthlight`.
const GH_REPOSITORY_ID = "1430923799"
const GH_OWNER_ID = "346862075"
const GH_WORKFLOWS = "stormlantern-dev/berthlight/.github/workflows"

def main [] {
  print "usage: gcp-release.nu <setup|import <pem>|aur-key <file>|fingerprint|status>"
}

def --wrapped exists [...args: string] {
  let r = (^gcloud ...$args --project $PROJECT --format "value(name)" --quiet | complete)
  if $r.exit_code == 0 { return true }
  if ($r.stderr | str contains "NOT_FOUND") or ($r.stderr | str contains "not found") { return false }
  error make { msg: $"gcloud ($args | str join ' ') failed:\n($r.stderr)" }
}

def kms-args [] { [--location $LOCATION --keyring $KEYRING] }

def provider-name [] {
  $"projects/($PROJECT_NUMBER)/locations/global/workloadIdentityPools/($GH_POOL)/providers/($GH_PROVIDER)"
}

def member [] {
  $"principalSet://iam.googleapis.com/projects/($PROJECT_NUMBER)/locations/global/workloadIdentityPools/($GH_POOL)/attribute.repository_id/($GH_REPOSITORY_ID)"
}

def condition [] {
  let release = ([
    $"assertion.job_workflow_ref.startsWith\('($GH_WORKFLOWS)/release.yml@refs/tags/v')"
    "assertion.ref.startsWith('refs/tags/v')"
    "assertion.event_name == 'push'"
  ] | str join " && ")
  let verify = ([
    $"assertion.job_workflow_ref == '($GH_WORKFLOWS)/verify-release-credentials.yml@refs/heads/main'"
    "assertion.ref == 'refs/heads/main'"
    "assertion.event_name == 'workflow_dispatch'"
  ] | str join " && ")
  ([
    $"assertion.repository_id == '($GH_REPOSITORY_ID)'"
    $"assertion.repository_owner_id == '($GH_OWNER_ID)'"
    $"\(\(($release)\) || \(($verify)\)\)"
  ] | str join " && ")
}

def "main setup" [] {
  (^gcloud services enable iam.googleapis.com iamcredentials.googleapis.com sts.googleapis.com
    cloudkms.googleapis.com secretmanager.googleapis.com --project $PROJECT)

  if not (exists iam workload-identity-pools describe $GH_POOL --location global) {
    print $"create pool ($GH_POOL)"
    (^gcloud iam workload-identity-pools create $GH_POOL --project $PROJECT --location global
      --display-name "GitHub Actions")
  }

  let mapping = ([
    "google.subject=assertion.sub"
    "attribute.repository_id=assertion.repository_id"
    "attribute.repository_owner_id=assertion.repository_owner_id"
    "attribute.ref=assertion.ref"
    "attribute.job_workflow_ref=assertion.job_workflow_ref"
  ] | str join ",")
  let r = (^gcloud iam workload-identity-pools providers describe $GH_PROVIDER --project $PROJECT
    --location global --workload-identity-pool $GH_POOL --format "value(state)" | complete)
  let verb = if $r.exit_code == 0 { "update-oidc" } else { "create-oidc" }
  print $"($verb) provider ($GH_PROVIDER)"
  (^gcloud iam workload-identity-pools providers $verb $GH_PROVIDER --project $PROJECT
    --location global --workload-identity-pool $GH_POOL
    --issuer-uri $GH_ISSUER
    --attribute-mapping $mapping
    --attribute-condition (condition))

  if not (exists kms keyrings describe $KEYRING --location $LOCATION) {
    print $"create keyring ($KEYRING)"
    ^gcloud kms keyrings create $KEYRING --location $LOCATION --project $PROJECT
  }
  # --import-only: every version is one somebody deliberately imported.
  if not (exists kms keys describe $KEY ...(kms-args)) {
    print $"create key ($KEY)"
    (^gcloud kms keys create $KEY ...(kms-args) --project $PROJECT
      --purpose asymmetric-signing --default-algorithm $ALGORITHM
      --protection-level software --import-only --skip-initial-version-creation)
  }
  # Signer on the KEY only: not the keyring, not the project.
  print $"signer: repository_id ($GH_REPOSITORY_ID) on ($KEY)"
  (^gcloud kms keys add-iam-policy-binding $KEY ...(kms-args) --project $PROJECT
    --role roles/cloudkms.signer --member (member) --format none)

  if not (exists secrets describe $SECRET) {
    print $"create secret ($SECRET)"
    (^gcloud secrets create $SECRET --project $PROJECT
      --replication-policy user-managed --locations $LOCATION)
  }
  # Accessor on the SECRET only.
  print $"accessor: repository_id ($GH_REPOSITORY_ID) on ($SECRET)"
  (^gcloud secrets add-iam-policy-binding $SECRET --project $PROJECT
    --role roles/secretmanager.secretAccessor --member (member) --format none)

  print ""
  print "berthlight repository variables:"
  print $"  TAP_WIF_PROVIDER       = (provider-name)"
  print $"  TAP_KMS_KEY_VERSION    = projects/($PROJECT)/locations/($LOCATION)/keyRings/($KEYRING)/cryptoKeys/($KEY)/cryptoKeyVersions/<n>   \(after import)"
  print $"  AUR_KEY_SECRET_VERSION = projects/($PROJECT)/secrets/($SECRET)/versions/latest"
}

# OPERATOR ONLY: credential material. Takes the .pem GitHub downloads when
# you generate a key on the App. The PKCS#8 DER copy KMS needs is written to
# a 0700 directory on tmpfs and removed whatever happens; delete the .pem
# yourself once `fingerprint` matches GitHub.
def "main import" [pem: path] {
  if not ($pem | path exists) { error make { msg: $"($pem) not found" } }

  let job = $"($KEY)-(date now | format date '%Y%m%d%H%M%S')"
  print $"create import job ($job)"
  (^gcloud kms import-jobs create $job ...(kms-args) --project $PROJECT
    --import-method rsa-oaep-3072-sha256-aes-256 --protection-level software)
  loop {
    let state = (^gcloud kms import-jobs describe $job ...(kms-args) --project $PROJECT --format "value(state)" | str trim)
    if $state == "ACTIVE" { break }
    print $"  import job ($state), waiting"
    sleep 5sec
  }

  let dir = (^mktemp -d -p ($env.XDG_RUNTIME_DIR? | default "/dev/shm") | str trim)
  ^chmod 700 $dir
  let der = ($dir | path join "key.der")
  try {
    ^openssl pkcs8 -topk8 -nocrypt -inform PEM -outform DER -in $pem -out $der
    with-env { CLOUDSDK_PYTHON_SITEPACKAGES: "1" } {
      (^gcloud kms keys versions import ...(kms-args) --project $PROJECT --key $KEY
        --import-job $job --algorithm $ALGORITHM --target-key-file $der)
    }
  } catch {|e|
    ^rm -rf $dir
    error make { msg: $"import failed: ($e.msg)" }
  }
  ^rm -rf $dir

  print ""
  print $"fingerprint of ($pem | path basename), as GitHub shows it:"
  print $"  (^openssl rsa -in $pem -pubout -outform DER | ^openssl sha256 -binary | ^openssl base64 | str trim)"
  main fingerprint
  print ""
  print "If they match: delete the .pem, set TAP_KMS_KEY_VERSION, run verify-release-credentials.yml."
}

# OPERATOR ONLY: credential material. Adds the AUR account's private SSH key
# as a new secret version; delete the file afterwards.
def "main aur-key" [file: path] {
  if not ($file | path exists) { error make { msg: $"($file) not found" } }
  let check = (^ssh-keygen -y -f $file | complete)
  if $check.exit_code != 0 { error make { msg: $"($file) is not a private key without passphrase" } }
  ^gcloud secrets versions add $SECRET --project $PROJECT --data-file $file
  print ""
  print $"public key, for the AUR account: ($check.stdout | str trim)"
  print "Delete the private key file now, then run verify-release-credentials.yml."
}

def "main fingerprint" [] {
  let versions = (^gcloud kms keys versions list ...(kms-args) --project $PROJECT --key $KEY --format json | from json)
  if ($versions | is-empty) { print "no key versions yet"; return }
  for v in $versions {
    let n = ($v.name | path basename)
    if $v.state != "ENABLED" { print $"  version ($n): ($v.state)"; continue }
    let pub = (^gcloud kms keys versions get-public-key $n ...(kms-args) --project $PROJECT --key $KEY)
    let fp = ($pub | ^openssl pkey -pubin -outform DER | ^openssl sha256 -binary | ^openssl base64 | str trim)
    print $"  version ($n): ($fp)"
  }
}

def "main status" [] {
  let r = (^gcloud iam workload-identity-pools providers describe $GH_PROVIDER --project $PROJECT
    --location global --workload-identity-pool $GH_POOL --format json | complete)
  if $r.exit_code == 0 {
    let p = ($r.stdout | from json)
    print $"provider ($GH_PROVIDER): ($p.state)"
    print $"  condition: ($p.attributeCondition)"
  } else {
    print $"provider ($GH_PROVIDER): false"
  }
  print $"keyring ($KEYRING): (exists kms keyrings describe $KEYRING --location $LOCATION)"
  if (exists kms keys describe $KEY ...(kms-args)) {
    print $"key ($KEY):"
    main fingerprint
    let policy = (^gcloud kms keys get-iam-policy $KEY ...(kms-args) --project $PROJECT --format json | from json)
    for b in ($policy | get -o bindings | default []) { print $"  ($b.role): ($b.members | str join ', ')" }
  } else {
    print $"key ($KEY): false"
  }
  if (exists secrets describe $SECRET) {
    let versions = (^gcloud secrets versions list $SECRET --project $PROJECT --format "value(name)" | lines | length)
    print $"secret ($SECRET): ($versions) version\(s)"
    let policy = (^gcloud secrets get-iam-policy $SECRET --project $PROJECT --format json | from json)
    for b in ($policy | get -o bindings | default []) { print $"  ($b.role): ($b.members | str join ', ')" }
  } else {
    print $"secret ($SECRET): false"
  }
}

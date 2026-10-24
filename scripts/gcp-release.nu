#!/usr/bin/env nu
# GCP side of berthlight's release credentials (PRD 0001, R9; issue #1), in
# the stormlantern-release project. The same pattern as
# dlouwers/experiments scripts/gcp-tap-kms.nu (#505), for this repository.
#
# Run from the repository root ON THE HOST, where gcloud is.
#
#   ./scripts/gcp-release.nu setup            # APIs, WIF pool and provider, keyring, import-only App key, AUR SSH key, bindings
#   ./scripts/gcp-release.nu import <pem>     # OPERATOR: import a key generated on the release App, then delete the .pem
#   ./scripts/gcp-release.nu aur-public-key   # the AUR SSH public key, for the AUR account
#   ./scripts/gcp-release.nu winget-token     # OPERATOR: store the winget machine user's token (prompts, no echo)
#   ./scripts/gcp-release.nu fingerprint      # GitHub-style fingerprint of the KMS public key(s)
#   ./scripts/gcp-release.nu status           # print what exists, change nothing
#
# ── What it grants ─────────────────────────────────────────────────────────
#
# GitHub does not accept federated identities: an installation token needs a
# JWT signed with the App's private key. So the key is imported into KMS
# (non-exportable) and release.yml proves who it is through GitHub OIDC →
# WIF to get permission to SIGN with it (.github/actions/app-token).
#
# The AUR only takes SSH keys. That key is generated IN KMS (ECDSA P-256)
# and never exists anywhere else: ssh signs with it through Google's PKCS#11
# library, kmsp11 (.github/actions/aur-ssh). It has a key ring of its own,
# because kmsp11 offers every key in the ring it is given.
#
# winget (R10) needs one bearer token after all: opening a pull request on
# microsoft/winget-pkgs takes a user's classic token (public_repo), because
# an App can only act where it is installed
# (.github/actions/winget-pr-token). It belongs to a machine user that owns
# nothing, and lives in Secret Manager as winget-pr-token, readable by the
# same WIF principal set and nothing else.
#
# Nothing else: no service account, no project-wide role.
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
# AUR key: no rotation is needed for exposure, since it cannot leave KMS.
# To replace it anyway, add a key version, put its public key on the AUR
# account next to the old one, verify, then remove the old public key and
# disable the old version (kmsp11 offers every enabled version).
# winget token: create a new classic token on the machine user, run
# `winget-token` (a new secret VERSION; WINGET_PR_TOKEN_SECRET names
# versions/latest), run verify-release-credentials.yml, then delete the old
# token on GitHub and disable the old version here.

const PROJECT = "stormlantern-release"
const PROJECT_NUMBER = "369937005182"
const LOCATION = "europe-west4"
const KEYRING = "github-apps"
const KEY = "release-app"  # the tap App (id 4637258), once it belongs to stormlantern-dev
const ALGORITHM = "rsa-sign-pkcs1-2048-sha256"  # RS256, what GitHub signs and verifies App JWTs with
const AUR_KEYRING = "aur-ssh"
const AUR_KEY = "aur"
const AUR_ALGORITHM = "ec-sign-p256-sha256"  # ecdsa-sha2-nistp256 in SSH; kmsp11 signs it with CKM_ECDSA
const WINGET_SECRET = "winget-pr-token"

const GH_POOL = "github-actions"
const GH_PROVIDER = "berthlight"
const GH_ISSUER = "https://token.actions.githubusercontent.com"
# Numeric ids: a re-created repository with the same name gets a new id and
# is NOT trusted. Read 2026-10-23 from `gh api repos/stormlantern-dev/berthlight`.
const GH_REPOSITORY_ID = "1430923799"
const GH_OWNER_ID = "346862075"
const GH_WORKFLOWS = "stormlantern-dev/berthlight/.github/workflows"

def main [] {
  print "usage: gcp-release.nu <setup|import <pem>|aur-public-key|winget-token|fingerprint|status>"
}

def --wrapped exists [...args: string] {
  let r = (^gcloud ...$args --project $PROJECT --format "value(name)" --quiet | complete)
  if $r.exit_code == 0 { return true }
  if ($r.stderr | str contains "NOT_FOUND") or ($r.stderr | str contains "not found") { return false }
  error make { msg: $"gcloud ($args | str join ' ') failed:\n($r.stderr)" }
}

def kms-args [] { [--location $LOCATION --keyring $KEYRING] }
def aur-args [] { [--location $LOCATION --keyring $AUR_KEYRING] }

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

  if not (exists kms keyrings describe $AUR_KEYRING --location $LOCATION) {
    print $"create keyring ($AUR_KEYRING)"
    ^gcloud kms keyrings create $AUR_KEYRING --location $LOCATION --project $PROJECT
  }
  # Generated here, never imported: the private key exists nowhere else.
  if not (exists kms keys describe $AUR_KEY ...(aur-args)) {
    print $"create key ($AUR_KEY)"
    (^gcloud kms keys create $AUR_KEY ...(aur-args) --project $PROJECT
      --purpose asymmetric-signing --default-algorithm $AUR_ALGORITHM
      --protection-level software)
  }
  # kmsp11 lists the ring's keys and versions (viewer, on the ring), reads
  # the public key and signs (signerVerifier, on the key).
  print $"kmsp11: repository_id ($GH_REPOSITORY_ID) on ($AUR_KEYRING)/($AUR_KEY)"
  (^gcloud kms keyrings add-iam-policy-binding $AUR_KEYRING --location $LOCATION --project $PROJECT
    --role roles/cloudkms.viewer --member (member) --format none)
  (^gcloud kms keys add-iam-policy-binding $AUR_KEY ...(aur-args) --project $PROJECT
    --role roles/cloudkms.signerVerifier --member (member) --format none)

  # Created empty: the operator adds the token (`winget-token`).
  if not (exists secrets describe $WINGET_SECRET) {
    print $"create secret ($WINGET_SECRET)"
    (^gcloud secrets create $WINGET_SECRET --project $PROJECT
      --replication-policy user-managed --locations $LOCATION)
  }
  # Accessor on the SECRET only.
  print $"accessor: repository_id ($GH_REPOSITORY_ID) on secret ($WINGET_SECRET)"
  (^gcloud secrets add-iam-policy-binding $WINGET_SECRET --project $PROJECT
    --role roles/secretmanager.secretAccessor --member (member) --format none)

  print ""
  print "berthlight repository variables:"
  print $"  TAP_WIF_PROVIDER       = (provider-name)"
  print $"  TAP_KMS_KEY_VERSION    = projects/($PROJECT)/locations/($LOCATION)/keyRings/($KEYRING)/cryptoKeys/($KEY)/cryptoKeyVersions/<n>   \(after import)"
  print $"  AUR_KMS_KEY_RING       = projects/($PROJECT)/locations/($LOCATION)/keyRings/($AUR_KEYRING)"
  print $"  WINGET_PR_TOKEN_SECRET = projects/($PROJECT)/secrets/($WINGET_SECRET)/versions/latest   \(after winget-token)"
  print ""
  print "AUR account public key (Account → SSH Public Key):"
  print $"  (aur-public-key)"
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

# OPERATOR ONLY: credential material. Stores the winget machine user's
# classic token (scope public_repo, nothing else) as a new version of the
# secret. It is read from the terminal without echo and goes to gcloud on
# stdin: never into a file, an argument or the shell history.
def "main winget-token" [] {
  let token = (input -s "classic token of the winget machine user (public_repo): " | str trim)
  print ""
  if ($token | is-empty) { error make { msg: "no token given" } }
  if not ($token | str starts-with "ghp_") {
    error make { msg: "not a classic personal access token (ghp_...): fine-grained tokens cannot open the pull request" }
  }
  $token | ^gcloud secrets versions add $WINGET_SECRET --project $PROJECT --data-file=- --format "value(name)"
  print "Set WINGET_PR_TOKEN_SECRET if it is not set, then run verify-release-credentials.yml."
}

# The AUR key's public half in OpenSSH format, for the AUR account.
def aur-public-key [] {
  let pem = (^gcloud kms keys versions get-public-key 1 ...(aur-args) --project $PROJECT --key $AUR_KEY)
  let file = (^mktemp | str trim)
  $pem | save -f $file
  let ssh = (^ssh-keygen -i -m PKCS8 -f $file | str trim)
  ^rm -f $file
  $"($ssh) berthlight-release@stormlantern-release"
}

def "main aur-public-key" [] { print (aur-public-key) }

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
  if (exists secrets describe $WINGET_SECRET) {
    let versions = (^gcloud secrets versions list $WINGET_SECRET --project $PROJECT --filter "state=ENABLED" --format "value(name)" | lines)
    print $"secret ($WINGET_SECRET): ($versions | length) enabled version\(s)"
    let policy = (^gcloud secrets get-iam-policy $WINGET_SECRET --project $PROJECT --format json | from json)
    for b in ($policy | get -o bindings | default []) { print $"  ($b.role): ($b.members | str join ', ')" }
  } else {
    print $"secret ($WINGET_SECRET): false"
  }
  if (exists kms keys describe $AUR_KEY ...(aur-args)) {
    print $"key ($AUR_KEYRING)/($AUR_KEY): (aur-public-key)"
    let ring = (^gcloud kms keyrings get-iam-policy $AUR_KEYRING --location $LOCATION --project $PROJECT --format json | from json)
    let key = (^gcloud kms keys get-iam-policy $AUR_KEY ...(aur-args) --project $PROJECT --format json | from json)
    for b in (($ring | get -o bindings | default []) ++ ($key | get -o bindings | default [])) {
      print $"  ($b.role): ($b.members | str join ', ')"
    }
  } else {
    print $"key ($AUR_KEYRING)/($AUR_KEY): false"
  }
}

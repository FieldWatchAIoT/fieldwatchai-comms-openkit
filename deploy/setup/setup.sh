#!/usr/bin/env bash
# Interactive first-run setup for a comms-openkit deployment.
#
# Creates the four things a working deployment needs — tenant, account, channel,
# and the link between account and channel — over HTTP, in dependency order,
# then asks the service to confirm the result. Every step is a call an operator
# could make by hand; this exists because getting the order and the field names
# right from the docs alone took longer than it should.
#
# Re-runnable: supply the same tenant id and it will reuse it.
#
# Usage:
#   deploy/setup/setup.sh                     # prompts for everything
#   CHANNELS_URL=... INTERNAL_API_TOKEN=... deploy/setup/setup.sh
set -euo pipefail

CHANNELS_URL="${CHANNELS_URL:-http://localhost:9090}"
INTERNAL_API_TOKEN="${INTERNAL_API_TOKEN:-}"

bold() { printf '\033[1m%s\033[0m\n' "$1"; }
warn() { printf '\033[33m%s\033[0m\n' "$1" >&2; }
die()  { printf '\033[31mERROR: %s\033[0m\n' "$1" >&2; exit 1; }

# ask <prompt> <default> — reads a value, falling back to the default on Enter.
# Reads stdin rather than /dev/tty so the script can also be driven from a file
# or heredoc. That is how it is tested, and it lets an operator script a repeat
# deployment instead of retyping answers.
ask() {
  local prompt="$1" default="${2:-}" reply
  if [ -n "$default" ]; then
    printf '%s [%s]: ' "$prompt" "$default" >&2
    read -r reply || true
    printf '%s' "${reply:-$default}"
  else
    printf '%s: ' "$prompt" >&2
    read -r reply || true
    printf '%s' "$reply"
  fi
}

# api <method> <path> [body] — calls the service, failing loudly on non-2xx.
# The response body is printed on failure: these endpoints return a "detail"
# field naming the offending field, which is the whole point of reading it.
api() {
  local method="$1" path="$2" body="${3:-}" out code
  local -a args=(-sS -m 20 -X "$method" "$CHANNELS_URL$path"
    -H "Authorization: Bearer $INTERNAL_API_TOKEN"
    -H 'Content-Type: application/json'
    -w '\n%{http_code}')
  [ -n "${TENANT_ID:-}" ] && args+=(-H "X-Tenant-ID: $TENANT_ID")
  [ -n "$body" ] && args+=(-d "$body")

  out=$(curl "${args[@]}") || die "could not reach $CHANNELS_URL$path"
  code=$(printf '%s' "$out" | tail -n1)
  out=$(printf '%s' "$out" | sed '$d')
  case "$code" in
    2*) printf '%s' "$out" ;;
    401) die "401 from $path — INTERNAL_API_TOKEN does not match the service's" ;;
    *)   die "$method $path returned $code: $out" ;;
  esac
}

jqf() { python3 -c "import json,sys; print(json.load(sys.stdin).get('$1',''))"; }

command -v curl   >/dev/null || die "curl is required"
command -v python3 >/dev/null || die "python3 is required (used to read JSON)"

bold "comms-openkit setup"
echo "Creates a tenant, an account, a channel, and the link between them."
echo

CHANNELS_URL=$(ask "channels URL" "$CHANNELS_URL")
[ -n "$INTERNAL_API_TOKEN" ] || INTERNAL_API_TOKEN=$(ask "INTERNAL_API_TOKEN (must match the service's)" "")
[ -n "$INTERNAL_API_TOKEN" ] || die "INTERNAL_API_TOKEN is required"

# Fail here rather than three calls later with a confusing error.
curl -fsS -m 10 "$CHANNELS_URL/healthz" >/dev/null 2>&1 \
  || die "$CHANNELS_URL/healthz is not answering. Is the stack up? (docker compose ps)"

echo
bold "1/5  Tenant"
TENANT_NAME=$(ask "organisation name" "My Agency")
EXISTING_TENANT=$(ask "existing tenant id (blank to create a new one)" "")
if [ -n "$EXISTING_TENANT" ]; then
  TENANT_ID="$EXISTING_TENANT"
  api GET "/v1/tenants/$TENANT_ID" >/dev/null
  echo "    reusing $TENANT_ID"
else
  TENANT_ID=$(api POST /v1/tenants "$(printf '{"name":%s}' "$(printf '%s' "$TENANT_NAME" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')")" | jqf id)
  echo "    created $TENANT_ID"
fi
export TENANT_ID

echo
bold "2/5  Account — the inbox messages arrive on"
echo "    The account TYPE is the platform, not the webhook listener id."
echo "    WhatsApp via UltraMSG is type 'whatsapp' (NOT 'whatsapp-ultramsg')."
ACCOUNT_TYPE=$(ask "type (whatsapp | sms-twilio | whatsapp-twilio | telegram | email-ses)" "whatsapp")
ACCOUNT_ID_EXT=$(ask "platform identifier (UltraMSG instance id, phone number, bot handle, or address)" "")
[ -n "$ACCOUNT_ID_EXT" ] || die "platform identifier is required — it is what inbound messages are matched on"
ACCOUNT_LABEL=$(ask "label" "$ACCOUNT_TYPE inbox")
echo "    Credentials are needed only to SEND. Leave blank to receive only."
ACCOUNT_CREDS=$(ask "credentials JSON (blank for none)" "")

ACCOUNT_BODY=$(ACCOUNT_TYPE="$ACCOUNT_TYPE" ACCOUNT_ID_EXT="$ACCOUNT_ID_EXT" ACCOUNT_LABEL="$ACCOUNT_LABEL" ACCOUNT_CREDS="$ACCOUNT_CREDS" python3 -c '
import json, os
b = {
  "type": os.environ["ACCOUNT_TYPE"], "owner_type": "tenant",
  "label": os.environ["ACCOUNT_LABEL"],
  "platform_identifier": os.environ["ACCOUNT_ID_EXT"],
  "status": "active", "capabilities": ["inbound", "outbound"],
}
if os.environ["ACCOUNT_CREDS"]:
    b["credentials"] = os.environ["ACCOUNT_CREDS"]
print(json.dumps(b))')
ACCOUNT_ID=$(api POST /v1/accounts "$ACCOUNT_BODY" | jqf id)
echo "    created $ACCOUNT_ID"

echo
bold "3/5  Channel — where the traffic is routed"
CHANNEL_NAME=$(ask "channel name" "Field Ops")
echo "    workflow_url is your system's webhook: where understood messages are POSTed."
echo "    Leave blank if you plan to read the database directly."
WORKFLOW_URL=$(ask "workflow_url (blank for none)" "")
CHANNEL_BODY=$(CHANNEL_NAME="$CHANNEL_NAME" WORKFLOW_URL="$WORKFLOW_URL" python3 -c '
import json, os
b = {"name": os.environ["CHANNEL_NAME"]}
if os.environ["WORKFLOW_URL"]:
    b["workflow_url"] = os.environ["WORKFLOW_URL"]
print(json.dumps(b))')
CHANNEL_ID=$(api POST /v1/channels "$CHANNEL_BODY" | jqf id)
echo "    created $CHANNEL_ID"

echo
bold "4/5  Link — without this the account receives messages and forwards nowhere"
api POST "/v1/channels/$CHANNEL_ID/accounts" \
  "$(printf '{"account_id":"%s","direction":"both","priority":100}' "$ACCOUNT_ID")" >/dev/null
echo "    linked account -> channel (direction=both)"

echo
bold "5/5  Verify"
api GET /v1/diagnostics > /tmp/openkit-setup-diag.json
python3 - <<'PY'
import json
r = json.load(open("/tmp/openkit-setup-diag.json"))
c = r["counts"]
print(f"    accounts={c['accounts']} channels={c['channels']} contacts={c['contacts']} messages={c['messages']}")
blocking = [f for f in r["findings"] if f["severity"] == "blocking"]
for f in r["findings"]:
    mark = "!!" if f["severity"] == "blocking" else " -"
    print(f"    {mark} {f['summary']}")
    print(f"       {f['remedy']}")
print()
print("    SETUP OK — no blocking problems." if not blocking else "    STILL BLOCKED — see above.")
PY
rm -f /tmp/openkit-setup-diag.json

cat <<EOF

$(bold "Next")
  Tenant id:   $TENANT_ID      <- every API call needs this in X-Tenant-ID
  Account id:  $ACCOUNT_ID
  Channel id:  $CHANNEL_ID

  Point your provider's webhook at your deployment's /inbound/<listener> URL,
  add contacts so short ids resolve:

    curl -X POST $CHANNELS_URL/v1/contacts \\
      -H 'Authorization: Bearer <token>' -H 'X-Tenant-ID: $TENANT_ID' \\
      -H 'Content-Type: application/json' \\
      -d '{"short_id":"42","display_name":"Marsh Harbour Shelter","status":"active"}'

  and re-check anytime with:

    curl -s $CHANNELS_URL/v1/diagnostics \\
      -H 'Authorization: Bearer <token>' -H 'X-Tenant-ID: $TENANT_ID'
EOF

#!/usr/bin/env bash
# Seeds the minimum rows the end-to-end demo needs, then exits.
#
# Why this exists: the webhook resolves every inbound message to an account by
# asking channels (GET /v1/accounts/lookup). With an empty database that lookup
# 404s and the webhook drops the message — so a fresh stack silently discards
# the payload from the README's curl. Three rows fix that.
#
# Why SQL rather than the HTTP API: `accounts.tenant_id` is a foreign key to
# `tenants`, and channels deliberately exposes no tenant-creation endpoint
# (tenancy is provisioned out of band). So the tenant row can only come from
# SQL, and doing the other two the same way keeps this script to one round trip.
# The API equivalents for accounts and contacts are in the repo README.
#
# Local demo only. It is idempotent, and it never runs against your own
# database unless you point it there.
set -euo pipefail

PSQL=(psql -h "${PGHOST:-postgres}" -U "${PGUSER:-openkit}" -d "${PGDATABASE:-openkit}" -v ON_ERROR_STOP=1 -q)

# channels applies its migrations on boot, so the schema appears a moment after
# the container starts. Wait for it rather than racing it.
echo "[seed] waiting for channels to apply migrations..."
for _ in $(seq 1 90); do
  if "${PSQL[@]}" -tAc "SELECT to_regclass('public.accounts') IS NOT NULL" 2>/dev/null | grep -qx 't'; then
    break
  fi
  sleep 2
done

if ! "${PSQL[@]}" -tAc "SELECT to_regclass('public.accounts') IS NOT NULL" 2>/dev/null | grep -qx 't'; then
  echo "[seed] ERROR: schema never appeared. Is the channels service healthy?" >&2
  echo "[seed]        try: docker compose logs channels" >&2
  exit 1
fi

# The account `type` is 'whatsapp', NOT 'whatsapp-ultramsg'. The webhook sends
# its listener id ('whatsapp-ultramsg') to /v1/accounts/lookup, and channels
# maps that to the stored platform type — see listenerToAccountType in
# internal/accounts/service.go. Storing the listener id here would make every
# lookup miss.
"${PSQL[@]}" <<'SQL'
INSERT INTO tenants (id, name, plan, created_at)
VALUES ('11111111-1111-1111-1111-111111111111', 'Demo Agency', 'starter', now())
ON CONFLICT (id) DO NOTHING;

INSERT INTO accounts (
  id, tenant_id, type, owner_type, label,
  platform_identifier, capabilities, status, created_at
)
VALUES (
  '22222222-2222-2222-2222-222222222222',
  '11111111-1111-1111-1111-111111111111',
  'whatsapp', 'tenant', 'Demo WhatsApp (UltraMSG)',
  'instance123', ARRAY['inbound','outbound'], 'active', now()
)
ON CONFLICT (type, platform_identifier) DO NOTHING;

-- short_id '42' is what makes the demo payload ("42 STATUS full") resolve at
-- full confidence and reach policy_action = 'execute'. Without a matching
-- contact the same message lands as 'clarify', which looks like a failure.
INSERT INTO contacts (
  id, tenant_id, short_id, display_name, role, status, created_at, updated_at
)
VALUES (
  '33333333-3333-3333-3333-333333333333',
  '11111111-1111-1111-1111-111111111111',
  '42', 'Marsh Harbour Shelter', 'shelter', 'active', now(), now()
)
ON CONFLICT (tenant_id, short_id) DO NOTHING;
SQL

echo "[seed] done. tenant=11111111-... account=22222222-... (whatsapp/instance123) contact=42"
echo "[seed]"
echo "[seed] send a message through the whole stack:"
echo "[seed]   curl -X POST 'http://localhost:8080/inbound/whatsapp-ultramsg?token=local-secret' \\"
echo "[seed]        -H 'Content-Type: application/json' \\"
echo "[seed]        --data @implementations/webhook-go/internal/listeners/ultramsg/testdata/text.json"

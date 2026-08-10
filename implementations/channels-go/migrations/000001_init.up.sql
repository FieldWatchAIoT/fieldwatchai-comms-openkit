-- FCC initial schema. The comms-channels service owns this schema (the first
-- FieldWatch Go repo to own its migrations). Tables are created in dependency
-- order. Multi-tenant from day one: every domain table carries tenant_id.

-- NOTE: PostGIS must be pre-provisioned in this database by the DBA/infra
-- (CREATE EXTENSION is a privileged operation; the app's migration role
-- intentionally cannot run it). messages.body_location (GEOGRAPHY) depends on
-- it. Locally the postgis Docker image installs it automatically.

CREATE TABLE tenants (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  plan TEXT NOT NULL,                 -- 'starter' | 'pro' | 'enterprise'
  created_at TIMESTAMPTZ NOT NULL,
  settings JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE accounts (
  id UUID PRIMARY KEY,
  tenant_id UUID REFERENCES tenants(id) NOT NULL,
  type TEXT NOT NULL,                 -- 'whatsapp' | 'sms' | 'email' | 'telegram' | 'spot' | 'zoleo_email' | 'garmin_email'
  owner_type TEXT NOT NULL,           -- 'platform' | 'tenant'   (managed vs BYO)
  label TEXT NOT NULL,                -- "WhatsApp #1 (Resilience)"
  platform_identifier TEXT NOT NULL,  -- phone number / email / bot handle / SPOT feed ID
  credentials_encrypted BYTEA,        -- platform API keys (encrypted via Encryptor)
  capabilities TEXT[] NOT NULL,       -- ['inbound', 'outbound'] for two-way; ['inbound'] for SPOT
  status TEXT NOT NULL,               -- 'active' | 'suspended' | 'banned'
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (type, platform_identifier)
);

CREATE TABLE channels (
  id UUID PRIMARY KEY,
  tenant_id UUID REFERENCES tenants(id) NOT NULL,
  name TEXT NOT NULL,                 -- "Resilience"
  parser_config JSONB NOT NULL,       -- grammar variant, command map, etc.
  workflow_url TEXT,                  -- consumer webhook (FieldWatch Resilience API)
  reply_policy TEXT NOT NULL,         -- 'reply_to_sender' | 'broadcast' | 'custom'
  confidence_thresholds JSONB NOT NULL, -- {high: 0.9, medium: 0.5}
  echo_back_enabled BOOL NOT NULL DEFAULT TRUE,
  recall_window_seconds INT NOT NULL DEFAULT 120,
  audit_retention_years INT NOT NULL DEFAULT 7,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE channel_accounts (
  channel_id UUID REFERENCES channels(id) NOT NULL,
  account_id UUID REFERENCES accounts(id) NOT NULL,
  direction TEXT NOT NULL,            -- 'inbound' | 'outbound' | 'both'
  priority INT NOT NULL,              -- for outbound: pick highest-priority
  routing_filter JSONB,               -- optional: subaddress / prefix rules
  PRIMARY KEY (channel_id, account_id)
);

CREATE TABLE contacts (
  id UUID PRIMARY KEY,
  tenant_id UUID REFERENCES tenants(id) NOT NULL,
  short_id TEXT NOT NULL,             -- "42" or "HMB" — what staff type
  display_name TEXT NOT NULL,         -- "Marsh Harbour Shelter"
  aoi_id TEXT,                        -- optional geographic grouping
  role TEXT,                          -- 'shelter' | 'shelter_manager' | 'rpic' | 'eoc' | 'staff'
  default_channel_id UUID REFERENCES channels(id),
  status TEXT NOT NULL DEFAULT 'active',
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tenant_id, short_id)
);

CREATE TABLE contact_endpoints (
  id UUID PRIMARY KEY,
  contact_id UUID REFERENCES contacts(id) ON DELETE CASCADE NOT NULL,
  channel_id UUID REFERENCES channels(id) NOT NULL,
  account_id UUID REFERENCES accounts(id) NOT NULL,
  endpoint TEXT NOT NULL,             -- phone / email / handle
  priority INT NOT NULL,              -- highest = preferred
  capabilities TEXT[] NOT NULL,       -- ['inbound', 'outbound']
  last_seen_at TIMESTAMPTZ,           -- when this endpoint last sent or replied
  failover_to_id UUID REFERENCES contact_endpoints(id)
);

CREATE TABLE groups (
  id UUID PRIMARY KEY,
  tenant_id UUID REFERENCES tenants(id) NOT NULL,
  name TEXT NOT NULL,
  type TEXT NOT NULL,                 -- 'aoi' | 'role' | 'tag' | 'static'
  query JSONB,                        -- e.g. {aoi_id: 'abaco'} for dynamic
  UNIQUE (tenant_id, name)
);

CREATE TABLE group_members (
  group_id UUID REFERENCES groups(id) ON DELETE CASCADE NOT NULL,
  contact_id UUID REFERENCES contacts(id) ON DELETE CASCADE NOT NULL,
  PRIMARY KEY (group_id, contact_id)
);

CREATE TABLE messages (
  id UUID PRIMARY KEY,
  tenant_id UUID REFERENCES tenants(id) NOT NULL,
  account_id UUID REFERENCES accounts(id),
  channel_id UUID REFERENCES channels(id),
  direction TEXT NOT NULL,            -- 'inbound' | 'outbound'
  sender_contact_id UUID REFERENCES contacts(id),
  recipient_contact_id UUID REFERENCES contacts(id),
  sender_endpoint TEXT,
  body_text TEXT NOT NULL,
  body_attachments JSONB,
  body_location GEOGRAPHY(Point, 4326),
  parsed JSONB,                       -- {short_id, command, target, payload, confidence, alternatives}
  policy_action TEXT,                 -- 'executed' | 'echoed_back' | 'clarified' | 'recalled' | 'dropped'
  workflow_fired BOOL NOT NULL DEFAULT FALSE,
  in_reply_to_message_id UUID REFERENCES messages(id),
  platform_message_id TEXT NOT NULL,
  raw_payload JSONB NOT NULL,
  received_at TIMESTAMPTZ NOT NULL,
  processed_at TIMESTAMPTZ,
  UNIQUE (account_id, platform_message_id)
);

CREATE TABLE workflows (
  id UUID PRIMARY KEY,
  channel_id UUID REFERENCES channels(id) NOT NULL,
  command TEXT NOT NULL,              -- 'STATUS' | 'NEEDS' | 'DAMAGE' | etc.
  action_type TEXT NOT NULL,          -- 'webhook' | 'broadcast' | 'reply' | 'noop'
  action_target TEXT,                 -- URL or template
  confirmation_required BOOL NOT NULL DEFAULT FALSE
);

CREATE TABLE audit_log (
  id BIGSERIAL PRIMARY KEY,
  tenant_id UUID REFERENCES tenants(id) NOT NULL,
  actor_type TEXT NOT NULL,           -- 'operator' | 'admin' | 'system'
  actor_id UUID,
  action TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id UUID,
  before JSONB,
  after JSONB,
  at TIMESTAMPTZ NOT NULL,
  metadata JSONB
);

CREATE INDEX messages_tenant_received_idx ON messages (tenant_id, received_at DESC);
CREATE INDEX messages_sender_contact_idx ON messages (sender_contact_id, received_at DESC);
CREATE INDEX contacts_short_id_idx ON contacts (tenant_id, short_id);
CREATE INDEX contacts_aoi_idx ON contacts (tenant_id, aoi_id);
CREATE INDEX audit_log_tenant_at_idx ON audit_log (tenant_id, at DESC);

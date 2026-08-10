-- Reverse 000001_init. Drop in reverse dependency order. The postgis extension
-- is intentionally NOT dropped: it is environment-managed (pre-provisioned on
-- the RDS cluster and on the local postgis image), shared, and may be required
-- by other objects.

DROP INDEX IF EXISTS audit_log_tenant_at_idx;
DROP INDEX IF EXISTS contacts_aoi_idx;
DROP INDEX IF EXISTS contacts_short_id_idx;
DROP INDEX IF EXISTS messages_sender_contact_idx;
DROP INDEX IF EXISTS messages_tenant_received_idx;

DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS workflows;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;
DROP TABLE IF EXISTS contact_endpoints;
DROP TABLE IF EXISTS contacts;
DROP TABLE IF EXISTS channel_accounts;
DROP TABLE IF EXISTS channels;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS tenants;

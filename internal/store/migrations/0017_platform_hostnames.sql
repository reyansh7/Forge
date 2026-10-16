-- Phase 7.9: hostnames Forge assigns under the operator's apps domain.
--
-- origin distinguishes a name the operator proved with DNS TXT (custom)
-- from a name Forge itself reserved from the application slug
-- ({slug}.{FORGE_APPS_DOMAIN}). Platform rows are inserted already
-- active: Forge is the authority for that zone, so the TXT challenge
-- would be the operator proving a name to themselves.
--
-- The unique hostname index from 0012 still applies. A custom domain
-- cannot take a platform name, and a second app cannot take the first
-- app's slug host. Existing rows default to custom, so 7.6 behavior
-- is unchanged until FORGE_APPS_DOMAIN is set.
ALTER TABLE application_domains
    ADD COLUMN origin TEXT NOT NULL DEFAULT 'custom'
        CHECK (origin IN ('custom', 'platform'));

-- Migration 450_user_account_status (originally 342, renumbered on each
-- upstream sync) defaults every user to account_status = 'active', which
-- silently re-enabled
-- the two production accounts previously blocked by the
-- deleted emergency denylist (server/internal/auth/temporary_disabled_users.go,
-- removed in 153183072 once account_status enforcement landed). Carry those
-- four identities (2 user IDs, 2 emails) forward into account_status so the
-- denylist's effect is preserved rather than lost.
UPDATE "user"
SET account_status = 'suspended', updated_at = now()
WHERE id IN ('514492f7-b30f-4147-bd33-c0e8ce5d6d4f', '1d542296-17c6-484a-9914-dcee589be116')
   OR lower(email) IN ('pdzzer68@embassybase.com', 'gtwtrox@mowan666.com');

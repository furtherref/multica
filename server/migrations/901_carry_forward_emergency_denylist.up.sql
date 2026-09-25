-- Migration 900_user_account_status (originally 342, renumbered on each
-- upstream sync) defaults every user to account_status = 'active', which
-- silently re-enabled
-- the two production accounts previously blocked by the
-- deleted emergency denylist (server/internal/auth/temporary_disabled_users.go,
-- removed in 153183072 once account_status enforcement landed). Carry those
-- four identities (2 user IDs, 2 emails) forward into account_status so the
-- denylist's effect is preserved rather than lost.
--
-- Renumbered with its sibling (343 -> 399 -> 442 -> 451 -> 537 -> 901). Unlike the
-- DDL migrations, replaying this UPDATE under a new stem is not a no-op: it
-- would re-suspend an account an admin has since restored. The ledger guard
-- makes a database that already ran any earlier stem skip the write.
UPDATE "user"
SET account_status = 'suspended', updated_at = now()
WHERE (id IN ('514492f7-b30f-4147-bd33-c0e8ce5d6d4f', '1d542296-17c6-484a-9914-dcee589be116')
       OR lower(email) IN ('pdzzer68@embassybase.com', 'gtwtrox@mowan666.com'))
  AND NOT EXISTS (
      SELECT 1 FROM schema_migrations
      WHERE version IN (
          '343_carry_forward_emergency_denylist',
          '399_carry_forward_emergency_denylist',
          '442_carry_forward_emergency_denylist',
          '451_carry_forward_emergency_denylist',
          '537_carry_forward_emergency_denylist'
      )
  );

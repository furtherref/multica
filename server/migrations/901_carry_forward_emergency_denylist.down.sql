UPDATE "user"
SET account_status = 'active', updated_at = now()
WHERE id IN ('514492f7-b30f-4147-bd33-c0e8ce5d6d4f', '1d542296-17c6-484a-9914-dcee589be116')
   OR lower(email) IN ('pdzzer68@embassybase.com', 'gtwtrox@mowan666.com');

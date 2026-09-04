-- Attach the CONCURRENTLY-built unique index as the table's primary key.
ALTER TABLE runtime_cost_budget
    ADD CONSTRAINT runtime_cost_budget_pkey PRIMARY KEY USING INDEX runtime_cost_budget_pkey_uidx;

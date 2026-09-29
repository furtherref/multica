-- Template ordering within a workspace. Mirrors the issue_status / issue_property
-- convention: position is a float so fractional moves are possible, and rows
-- order by position then name for a stable tiebreak.
ALTER TABLE issue_template
    ADD COLUMN position FLOAT NOT NULL DEFAULT 0;

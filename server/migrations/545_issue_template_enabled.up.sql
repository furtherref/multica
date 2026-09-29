-- Issue templates can be disabled without archiving: a disabled template is
-- hidden from the create-issue picker but stays visible in the management
-- list so it can be re-enabled. Default is enabled (true) so existing
-- templates keep their current picker behavior.
ALTER TABLE issue_template
    ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT true;

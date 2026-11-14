-- Fork migration: the fork's archive status (#39, migration 069) in the
-- sub-issue change log. Upstream's record_issue_child_event() (558) reads
-- "closed" from the built-in done/cancelled keys and the status catalog;
-- archive has no catalog row, so a sub-issue moving between an open status and
-- archive records nothing there and the parent's child_done rule and sub-issue
-- conditions never re-evaluate. This companion trigger records exactly those
-- moves, so archive closes a sub-issue like every other closed-lifecycle check
-- on the server (issuestatus maps archive to the closed category).
--
-- A move between archive and another closed status records nothing here: the
-- closed set did not change. Upstream's trigger still logs closed/reopened for
-- it, which only prompts an evaluation that reads archive as closed.
--
-- A separate trigger rather than a redefinition of upstream's function, so a
-- later upstream migration that redefines record_issue_child_event() cannot
-- silently drop archive on databases that already ran this file.
CREATE OR REPLACE FUNCTION record_issue_child_archive_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE other text;
BEGIN
 other := CASE WHEN NEW.status='archive' THEN OLD.status ELSE NEW.status END;
 IF other IN ('done','cancelled') OR EXISTS(SELECT 1 FROM issue_status s WHERE s.workspace_id=NEW.workspace_id AND s.key=other AND s.category IN ('done','closed')) THEN
  RETURN NULL;
 END IF;
 INSERT INTO issue_child_event(workspace_id,parent_id,child_id,kind,source_task_id)
 VALUES(NEW.workspace_id,NEW.parent_issue_id,NEW.id,CASE WHEN NEW.status='archive' THEN 'closed' ELSE 'reopened' END,
  NULLIF(current_setting('multica.source_task_id',true),'')::uuid);
 RETURN NULL;
END $$;

-- A write that also moves the sub-issue to another parent is recorded by
-- upstream's trigger as detached/attached, which ignores the status change;
-- this trigger stays out of it the same way.
DROP TRIGGER IF EXISTS issue_child_event_archive_update ON issue;
CREATE TRIGGER issue_child_event_archive_update AFTER UPDATE OF status ON issue FOR EACH ROW
 WHEN (NEW.parent_issue_id IS NOT NULL AND OLD.parent_issue_id IS NOT DISTINCT FROM NEW.parent_issue_id
  AND OLD.status IS DISTINCT FROM NEW.status AND (OLD.status='archive' OR NEW.status='archive'))
 EXECUTE FUNCTION record_issue_child_archive_event();

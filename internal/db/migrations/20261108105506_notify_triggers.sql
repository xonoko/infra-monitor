-- Live fan-out: every replica LISTENs and pushes to its SSE clients / wakes its workers.
CREATE OR REPLACE FUNCTION infra_monitor_notify_timeline() RETURNS trigger AS $$
BEGIN
  PERFORM pg_notify('im_timeline', json_build_object('id', NEW.id, 'incident_id', NEW.incident_id, 'type', NEW.type)::text);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER timeline_events_notify AFTER INSERT ON "timeline_events"
  FOR EACH ROW EXECUTE FUNCTION infra_monitor_notify_timeline();

CREATE OR REPLACE FUNCTION infra_monitor_notify_job() RETURNS trigger AS $$
BEGIN
  IF NEW.status = 'pending' AND NEW.run_at <= now() THEN
    PERFORM pg_notify('im_jobs', NEW.kind);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER jobs_notify AFTER INSERT ON "jobs"
  FOR EACH ROW EXECUTE FUNCTION infra_monitor_notify_job();

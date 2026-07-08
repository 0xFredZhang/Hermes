-- At most one active (queued or running) job per environment. This is the
-- atomic backstop for the orchestrator's HasActiveJob check-then-act guard:
-- a concurrent enqueue that races past the check fails on this constraint.
CREATE UNIQUE INDEX one_active_job_per_env
    ON jobs (environment_id)
    WHERE status IN ('queued', 'running');

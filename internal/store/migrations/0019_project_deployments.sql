-- A project deployment is a saga, not a database transaction.
--
-- Containers, image builds, and Caddy changes cannot commit or roll
-- back atomically. This row is the immutable plan plus the enqueue
-- outcome. Each service row points at the existing deployments row
-- that the worker already knows how to build, health-check, and
-- cut over. Overall success is derived from those child rows: a
-- required service that is not live means the project deployment
-- is not live.
--
-- plan is the discovery snapshot taken when Deploy All was pressed
-- (roots, packages, ports, suggested links). Later edits to the
-- repository do not change a rollout that has already started.

CREATE TABLE IF NOT EXISTS project_deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    status TEXT NOT NULL
        CHECK (status IN ('deploying', 'failed', 'live')),
    error_message TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    plan JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS project_deployments_project_idx
    ON project_deployments (project_id, created_at DESC);

CREATE TABLE IF NOT EXISTS project_deployment_services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_deployment_id UUID NOT NULL REFERENCES project_deployments (id) ON DELETE CASCADE,
    application_id UUID NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    deployment_id UUID REFERENCES deployments (id) ON DELETE SET NULL,
    service_name TEXT NOT NULL
        CHECK (char_length(service_name) BETWEEN 1 AND 100),
    root_directory TEXT NOT NULL DEFAULT '.',
    build_package TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL
        CHECK (status IN ('queued', 'failed')),
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS project_deployment_services_rollout_idx
    ON project_deployment_services (project_deployment_id);

CREATE INDEX IF NOT EXISTS project_deployment_services_deployment_idx
    ON project_deployment_services (deployment_id);

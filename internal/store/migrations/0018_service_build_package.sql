-- A Go module can contain several programs (cmd/api, cmd/worker).
-- root_directory stays the module root so go.mod is visible.
-- build_package is the single package the next deploy builds, for
-- example ./cmd/api. Empty means the detector chooses, which is the
-- previous behavior for a repository with one main package.
ALTER TABLE applications
    ADD COLUMN IF NOT EXISTS build_package TEXT NOT NULL DEFAULT '';

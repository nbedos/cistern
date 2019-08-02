package cache

const CurrentSchema = `
CREATE TABLE vcs_commit
(
    id          INTEGER PRIMARY KEY,
    sha         TEXT NOT NULL,
    ref         TEXT,
    message     TEXT NOT NULL,
    compare_url TEXT NOT NULL
);

CREATE TABLE job_config
(
    id         INTEGER PRIMARY KEY,
    name       TEXT,
    os         TEXT,
    os_distrib TEXT,
    lang       TEXT,
    env        TEXT,
    json       TEXT NOT NULL UNIQUE
);

CREATE TABLE build
(
    id                INTEGER PRIMARY KEY,
    state             TEXT    NOT NULL,
    repository        TEXT    NOT NULL,
    repo_build_number TEXT    NOT NULL,
    commit_id         INTEGER NOT NULL,
    FOREIGN KEY (commit_id) REFERENCES vcs_commit (id)
);

CREATE TABLE stage
(
    id       INTEGER PRIMARY KEY,
    build_id INTEGER NOT NULL,
    number   TEXT    NOT NULL,
    name     TEXT    NOT NULL,
    state    TEXT    NOT NULL,
    FOREIGN KEY (build_id) REFERENCES build (id)
);

CREATE TABLE job
(
    id              INTEGER PRIMARY KEY,
    stage_id        INTEGER NOT NULL,
    state           TEXT    NOT NULL,
    repository      TEXT    NOT NULL,
    repo_job_number TEXT    NOT NULL,
    log             TEXT    NOT NULL,
    config_id       INTEGER NOT NULL,
    FOREIGN KEY (stage_id) REFERENCES stage (id),
    FOREIGN KEY (config_id) REFERENCES job_config (id)
);
`

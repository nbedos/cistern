package cache

/*
	TODO url + user_id may be a good primary key for 'account'
 */

const CurrentSchema = `
CREATE TABLE account
(
    id       TEXT PRIMARY KEY,
    url      TEXT NOT NULL,
    user_id  TEXT NOT NULL,
    username TEXT NOT NULL,
    UNIQUE (url, user_id, username)
);

CREATE TABLE vcs_repository
(
    account_id TEXT NOT NULL,
    id         INTEGER NOT NULL,
    name       TEXT    NOT NULL,
    url        TEXT    NOT NULL,
    PRIMARY KEY (account_id, id),
	FOREIGN KEY (account_id) REFERENCES account (id)
);

CREATE TABLE vcs_commit
(
    account_id    TEXT NOT NULL,
    id            TEXT NOT NULL,
    repository_id INTEGER NOT NULL,
    message       TEXT    NOT NULL,
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id, repository_id) REFERENCES vcs_repository (account_id, id)
);

CREATE TABLE build
(
    account_id        TEXT NOT NULL,
    id                INTEGER NOT NULL,
    commit_id         INTEGER NOT NULL,
    repo_build_number TEXT    NOT NULL,
    state             TEXT    NOT NULL,
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id, commit_id) references vcs_commit (account_id, id)
);

CREATE TABLE stage
(
    account_id TEXT NOT NULL,
    id         INTEGER NOT NULL,
    build_id   INTEGER NOT NULL,
    number     TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    state      TEXT    NOT NULL,
    PRIMARY KEY (account_id, id, build_id),
    FOREIGN KEY (account_id, build_id) references build (account_id, id)
);

CREATE TABLE job
(
    account_id      TEXT NOT NULL,
    id              INTEGER NOT NULL,
    build_id        INTEGER NOT NULL,
    stage_id        INTEGER,
    state           TEXT    NOT NULL,
    repo_job_number TEXT    NOT NULL,
    log             TEXT    NOT NULL,
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id, build_id) references build (account_id, id),
    FOREIGN KEY (account_id, stage_id, build_id) references stage (account_id, id, build_id)
);
`
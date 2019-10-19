package cache

const CurrentSchema = `
-- FIXME Normalize state column

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
    url        TEXT NOT NULL,
    owner      TEXT NOT NULL,
	name       TEXT NOT NULL,
	remote_id  INTEGER NOT NULL,
	PRIMARY KEY (account_id, url),
    FOREIGN KEY (account_id) REFERENCES account(id) 
);

CREATE TABLE vcs_commit
(
	account_id     TEXT NOT NULL,
	repository_url TEXT    NOT NULL,
    id             TEXT    NOT NULL,
    message        TEXT    NOT NULL,
	date           TEXT,
    PRIMARY KEY (account_id, repository_url, id),
    FOREIGN KEY (account_id, repository_url) REFERENCES vcs_repository (account_id, url)
);

CREATE TABLE state
(
	name TEXT NOT NULL,
	PRIMARY KEY(name)
);

INSERT INTO state(name)
VALUES ('?'), ('pending'), ('running'), ('passed'), ('failed'), ('canceled'), ('skipped');

CREATE TABLE build
(
    account_id        TEXT NOT NULL,
    id                INTEGER NOT NULL CHECK(id > 0),
	repository_url    TEXT NOT NULL,
    commit_id         INTEGER NOT NULL,
	ref				  TEXT NOT NULL, -- Git reference of the commit
	is_tag			  INTEGER NOT NULL, -- Boolean set to true if the reference is a tag
    repo_build_number TEXT    NOT NULL,
    state             TEXT    NOT NULL,
	created_at        TEXT,
	started_at        TEXT,
	finished_at       TEXT,
	updated_at        TEXT NOT NULL,
	duration          INTEGER,
	web_url           TEXT,
    PRIMARY KEY (account_id, id),
	FOREIGN KEY (state) references state(name),
    FOREIGN KEY (account_id, repository_url, commit_id) references vcs_commit (account_id, repository_url, id)
);

CREATE TABLE stage
(
    account_id TEXT NOT NULL,
    build_id   INTEGER NOT NULL,
	id         INTEGER NOT NULL CHECK(id > 0), -- Number of the stage for this build
    name       TEXT    NOT NULL,
    state      TEXT    NOT NULL,
    PRIMARY KEY (account_id, build_id, id),
	FOREIGN KEY (state) references state(name),
    FOREIGN KEY (account_id, build_id) references build (account_id, id) ON DELETE CASCADE
);

CREATE TABLE job
(
    account_id      TEXT NOT NULL,
	build_id        INTEGER NOT NULL,
	stage_id        INTEGER NOT NULL,
    id              INTEGER NOT NULL CHECK(id > 0), -- Number of the job for this stage
    state           TEXT    NOT NULL,
	name            TEXT    NOT NULL,
	created_at      TEXT,
	started_at      TEXT,
	finished_at     TEXT,
	duration        INTEGER,
    log             TEXT,
	web_url         TEXT,
	remote_id       INTEGER,
    PRIMARY KEY (account_id, build_id, stage_id, id),
	FOREIGN KEY (state) references state(name),
    FOREIGN KEY (account_id, build_id, stage_id) references stage (account_id, build_id, id)  ON DELETE CASCADE
);

-- FIXME Deduplicate job and build_job
CREATE TABLE build_job
(
    account_id      TEXT NOT NULL,
	build_id        INTEGER NOT NULL,
    id              INTEGER NOT NULL CHECK(id > 0), -- Number of the job for this stage
    state           TEXT    NOT NULL,
	name            TEXT    NOT NULL,
	created_at      TEXT,
	started_at      TEXT,
	finished_at     TEXT,
	duration        INTEGER,
    log             TEXT,
	web_url         TEXT,
	remote_id       INTEGER,
    PRIMARY KEY (account_id, build_id, id),
	FOREIGN KEY (state) references state(name),
    FOREIGN KEY (account_id, build_id) references build (account_id, id)  ON DELETE CASCADE
);

`

package cache

/*
	TODO:
		Add busy timeout as in https://github.com/mattn/go-sqlite3/issues/274#issuecomment-232942571
		READ ONLY MODE for offline use

*/

import (
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"os"
)

type Inserter interface {
	insert(transaction *sql.Tx) (sql.Result, error)
}

type Account struct {
	Id       string
	Url      string
	UserId   string
	Username string
}

type Repository struct {
	AccountId string
	Id        int
	Name      string
	Url       string
}

type Commit struct {
	AccountId    string
	Id           string
	RepositoryId int
	Message      string
	Builds       []Build
}

type Build struct {
	AccountId       string
	Id              int
	CommitId        string
	State           string
	RepoBuildNumber string
}

type Stage struct {
	AccountId string
	Id        int
	BuildId   int
	Number    int
	Name      string
	State     string
}

type Job struct {
	AccountId string
	Id        int
	BuildId   int
	StageId   *int
	State     string
	Number    string
	Log       string
}

type Cache struct {
	FilePath  string
	connected bool
	db        sql.DB
}

func (c *Cache) connect(removeExisting bool) (err error) {
	if c.connected {
		return nil
	}

	if removeExisting {
		if err := os.Remove(c.FilePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	// FIXME Properly escape filepaths
	uri := fmt.Sprintf("%s?_foreign_keys=on", c.FilePath)
	db, err := sql.Open("sqlite3", uri)
	if err != nil {
		return err
	}
	defer func() {
		// Close the db in case of failure
		if err != nil {
			_ = db.Close()
		}
	}()

	// Check if the database was just created by the Open() call
	var count int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' ;").Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		if _, err = db.Exec(CurrentSchema); err != nil {
			return err
		}
	}

	c.db = *db
	c.connected = true
	return nil
}

func (c *Cache) Close() (err error) {
	if c.connected {
		err = c.db.Close()
		c.connected = false
	}
	return
}

func (c *Cache) Save(i Inserter) (err error) {
	if !c.connected {
		err = c.connect(true)
		if err != nil {
			return err
		}
	}
	transaction, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			_ = transaction.Rollback()
			panic(r)
		} else if err == nil {
			err = transaction.Commit()
		} else {
			_ = transaction.Rollback()
		}
	}()

	_, err = i.insert(transaction)

	return err
}

func (a Account) insert(transaction *sql.Tx) (sql.Result, error) {
	// TODO: Rely on autoincrement for the value of 'id'
	return transaction.Exec(
		"INSERT INTO account(id, url, user_id, username) VALUES (:id, :url, :user_id, :username);",
		sql.Named("id", a.Id),
		sql.Named("url", a.Url),
		sql.Named("user_id", a.UserId),
		sql.Named("username", a.Username),
	)
}

func (r Repository) insert(transaction *sql.Tx) (sql.Result, error) {
	return transaction.Exec(
		"INSERT INTO vcs_repository(account_id, id, name, url) VALUES (:account_id, :id, :name, :url);",
		sql.Named("account_id", r.AccountId),
		sql.Named("id", r.Id),
		sql.Named("name", r.Name),
		sql.Named("url", r.Url),
	)
}

func (c Commit) insert(transaction *sql.Tx) (sql.Result, error) {
	return transaction.Exec(
		"INSERT INTO vcs_commit(account_id, id, repository_id, message) VALUES (:account_id, :id, :repository_id, :message);",
		sql.Named("account_id", c.AccountId),
		sql.Named("id", c.Id),
		sql.Named("repository_id", c.RepositoryId),
		sql.Named("message", c.Message),
	)
}

func (b Build) insert(transaction *sql.Tx) (sql.Result, error) {
	return transaction.Exec(
		"INSERT INTO build(account_id, id, commit_id, repo_build_number, state) VALUES (:account_id, :id, :commit_id, :repo_build_number, :state);",
		sql.Named("account_id", b.AccountId),
		sql.Named("id", b.Id),
		sql.Named("commit_id", b.CommitId),
		sql.Named("repo_build_number", b.RepoBuildNumber),
		sql.Named("state", b.State),
	)
}

func (s Stage) insert(transaction *sql.Tx) (sql.Result, error) {
	return transaction.Exec(
		"INSERT INTO stage(account_id, id, build_id, number, name, state) VALUES (:account_id, :id, :build_id, :number, :name, :state);",
		sql.Named("account_id", s.AccountId),
		sql.Named("id", s.Id),
		sql.Named("build_id", s.BuildId),
		sql.Named("number", s.Number),
		sql.Named("name", s.Name),
		sql.Named("state", s.State),
	)
}

func (j Job) insert(transaction *sql.Tx) (sql.Result, error) {
	var stageId sql.NullInt64
	if j.StageId != nil {
		stageId.Valid = true
		stageId.Int64 = int64(*j.StageId)
	}
	return transaction.Exec(
		"INSERT INTO job(account_id, id, build_id, stage_id, state, repo_job_number, log) VALUES (:account_id, :id, :build_id, :stage_id, :state, :repo_job_number, :log);",
		sql.Named("account_id", j.AccountId),
		sql.Named("id", j.Id),
		sql.Named("build_id", j.BuildId),
		sql.Named("stage_id", stageId),
		sql.Named("state", j.State),
		sql.Named("repo_job_number", j.Number),
		sql.Named("log", j.Log),
	)
}

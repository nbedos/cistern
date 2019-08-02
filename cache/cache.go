package cache

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"log"
	"os"
)

type Commit struct {
	Id         int
	Sha        string
	Ref        string
	Message    string
	CompareUrl string
}

type Config struct {
	Name     string
	Os       string
	Distrib  string
	Language string
	Env      string
	Json     string
}

type Job struct {
	Id     int
	State  string
	Config Config
	Number string
	Log    string
}

type Stage struct {
	Id     int
	Number int
	Name   string
	State  string
	Jobs   []Job
}

type Build struct {
	Id              int
	State           string
	Repository      string
	RepoBuildNumber string
	Commit          Commit
	Stages          []Stage
}

type Cache struct {
	FilePath string
	connected bool
	db sql.DB
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

	db, err := sql.Open("sqlite3", c.FilePath)
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

func (c *Cache) SaveBuild(build Build) (err error) {
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

	_, err = c.insertBuild(transaction, build)

	return err
}

func (c Cache) SaveBuilds(builds <-chan Build) {
	for {
		if err := c.SaveBuild(<-builds); err != nil {
			log.Fatal(err)
		}
	}
}

func (Cache) insertCommit(transaction *sql.Tx, c Commit) (sql.Result, error) {
	const insertQuery = "INSERT INTO vcs_commit(id, sha, ref, message, compare_url) VALUES (?, ?, ?, ?, ?);"
	return transaction.Exec(insertQuery, c.Id, c.Sha, c.Ref, c.Message, c.CompareUrl)
}

func (Cache) insertConfig(transaction *sql.Tx, c Config) (sql.Result, error) {
	const insertQuery = `
		INSERT OR IGNORE INTO job_config(name, os, os_distrib, lang, env, json) 
		VALUES (?, ?, ?, ?, ?, ?);
	`
	return transaction.Exec(insertQuery, c.Name, c.Os, c.Distrib, c.Language, c.Env, c.Json)
}

func (c Cache) insertJob(transaction *sql.Tx, j Job, stageId int) (sql.Result, error) {
	const insertQuery = `
		INSERT INTO job(id, stage_id, state, repository, repo_job_number, log, config_id)
		VALUES (?, ?, ?, ?, ?, ?, ?);
	`
	res, err := c.insertConfig(transaction, j.Config)
	if err != nil {
		return res, err
	}

	configId, err := res.LastInsertId()
	if err != nil {
		return res, err
	}
	return transaction.Exec(insertQuery, j.Id, stageId, j.State, "", j.Number, j.Log, configId)
}

func (c Cache) insertStage(transaction *sql.Tx, s Stage, buildId int) (sql.Result, error) {
	const insertQuery = "INSERT INTO stage(id, build_id, number, name, state) VALUES (?, ?, ?, ?, ?);"
	for _, job := range s.Jobs {
		res, err := c.insertJob(transaction, job, s.Id)
		if err != nil {
			return res, err
		}
	}

	return transaction.Exec(insertQuery, s.Id, buildId, s.Number, s.Name, s.State)
}

func (c Cache) insertBuild(transaction *sql.Tx, b Build) (sql.Result, error) {
	const insertQuery = "INSERT INTO build(id, state, repository, repo_build_number, commit_id) VALUES (?, ?, ?, ?, ?);"
	res, err := c.insertCommit(transaction, b.Commit)
	if err != nil {
		return res, err
	}
	for _, stage := range b.Stages {
		res, err := c.insertStage(transaction, stage, b.Id)
		if err != nil {
			return res, err
		}
	}
	return transaction.Exec(insertQuery, b.Id, b.State, b.Repository, b.RepoBuildNumber, b.Commit.Id)
}

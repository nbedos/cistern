package providers

import (
	"bytes"
	"errors"
	"github.com/nbedos/citop/cache"
	"github.com/xanzy/go-gitlab"
)

type GitLabClient struct {
	Account cache.Account
	Remote  *gitlab.Client
}

func NewGitLabClient(account cache.Account, token string) GitLabClient {
	return GitLabClient{
		Account: account,
		Remote:  gitlab.NewClient(nil, token),
	}
}

func (c GitLabClient) GetUserBuilds(token string, user string) ([]cache.Inserter, error) {
	var inserters []cache.Inserter

	opt := &gitlab.ListProjectsOptions{Search: gitlab.String(user)}

	projects, _, err := c.Remote.Projects.ListProjects(opt)
	if err != nil {
		return inserters, err
	}

	for _, project := range projects {
		repository := cache.Repository{
			AccountId: c.Account.Id,
			Id:        project.ID,
			Name:      project.Name,
			Url:       project.WebURL,
		}

		inserters = append(inserters, repository)

		allCommits := true
		commits, _, err := c.Remote.Commits.ListCommits(project.ID, &gitlab.ListCommitsOptions{All: &allCommits})
		if err != nil {
			return inserters, err
		}
		commitShaToId := make(map[string]bool)
		for _, commit := range commits {
			cacheCommit := cache.Commit{
				AccountId:    c.Account.Id,
				Id:           commit.ID,
				RepositoryId: project.ID,
				Message:      commit.Message,
			}
			inserters = append(inserters, cacheCommit)
			commitShaToId[cacheCommit.Id] = true
		}

		pipelines, _, err := c.Remote.Pipelines.ListProjectPipelines(project.ID, nil)
		if err != nil {
			return inserters, err
		}
		for _, pipeline := range pipelines {
			if _, ok := commitShaToId[pipeline.SHA]; !ok {
				err = errors.New("unknown commit")
				return inserters, err
			}
			build := cache.Build{
				AccountId:       c.Account.Id,
				Id:              pipeline.ID,
				CommitId:        pipeline.SHA,
				State:           pipeline.Status,
				RepoBuildNumber: "",
			}
			inserters = append(inserters, build)
			jobs, _, err := c.Remote.Jobs.ListPipelineJobs(project.ID, pipeline.ID, nil)
			if err != nil {
				return inserters, err
			}
			for _, job := range jobs {
				reader, _, err := c.Remote.Jobs.GetTraceFile(project.ID, job.ID, nil)
				if err != nil {
					return inserters, err
				}
				buf := new(bytes.Buffer)
				_, err = buf.ReadFrom(reader)
				if err != nil {
					return inserters, err
				}

				cacheJob := cache.Job{
					AccountId: c.Account.Id,
					Id:        job.ID,
					BuildId:   pipeline.ID,
					StageId:   nil,
					State:     job.Status,
					Number:    "",
					Log:       buf.String(),
				}

				inserters = append(inserters, cacheJob)
			}
		}
	}

	return inserters, err
}

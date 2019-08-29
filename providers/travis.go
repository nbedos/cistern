package providers

import (
	"context"
	"github.com/nbedos/citop/cache"
	"github.com/shuheiktgw/go-travis"
)

type TravisClient struct {
	Account cache.Account
	Remote  *travis.Client
}

const TravisApiOrgUrl = travis.ApiOrgUrl
const TravisApiComUrl = travis.ApiComUrl

func NewTravisClient(account cache.Account, token string) TravisClient {
	return TravisClient{
		Account: account,
		Remote:  travis.NewClient(account.Url, token),
	}
}

func (c TravisClient) GetUserBuilds(token string, user string) ([]cache.Inserter, error) {
	var inserters []cache.Inserter

	opt := travis.BuildsOption{
		Include: []string{
			"build.stages",
			"stage.jobs",
			"job.config",
		},
		Limit: 10,
	}
	// TODO: Context should be an argument of this function
	builds, _, err := c.Remote.Builds.List(context.Background(), &opt)
	if err != nil {
		return inserters, err
	}

	for _, build := range builds {
		if build.Repository.Id == nil || build.Number == nil || build.Repository.Name == nil || build.Commit.Sha == nil || build.State == nil || build.Id == nil {
			continue
		}

		repository := cache.Repository{
			AccountId: c.Account.Id,
			Id:        int(*build.Repository.Id),
			Name:      *build.Repository.Name,
			Url:       "",
		}
		inserters = append(inserters, repository)

		commit := cache.Commit{
			AccountId:    c.Account.Id,
			Id:           *build.Commit.Sha,
			RepositoryId: repository.Id,
			Message:      *build.Commit.Message,
		}
		inserters = append(inserters, commit)

		cacheBuild := cache.Build{
			AccountId:       c.Account.Id,
			Id:              int(*build.Id),
			CommitId:        commit.Id,
			State:           *build.State,
			RepoBuildNumber: *build.Number,
		}
		inserters = append(inserters, cacheBuild)

		for idx, stage := range build.Stages {
			if stage.Id == nil || stage.Name == nil || stage.State == nil {
				continue
			}

			cacheStage := cache.Stage{
				AccountId: cacheBuild.AccountId,
				Id: int(*stage.Id),
				BuildId: cacheBuild.Id,
				Number: idx,
				Name: *stage.Name,
				State: *stage.State,
			}
			inserters = append(inserters, cacheStage)

			for _, job := range stage.Jobs {
				if job.Id == nil || job.State == nil || job.Number == nil{
					continue
				}

				// FIXME Context should be an argument of the current function
				log, _, err := c.Remote.Logs.FindByJobId(context.Background(), *job.Id)
				if err != nil {
					return inserters, err
				}

				if log.Content == nil {
					continue
				}

				cacheJob := cache.Job{
					AccountId: cacheStage.AccountId,
					Id: int(*job.Id),
					BuildId: cacheBuild.Id,
					//FIXME There may not be any stage
					StageId: &cacheStage.Id,
					State: *job.State,
					Number: *job.Number,
					Log: *log.Content,
				}
				inserters = append(inserters, cacheJob)
			}
		}
	}

	return inserters, err
}

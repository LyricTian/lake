package tasks

import (
	lakeModels "github.com/merico-dev/lake/models"
	"github.com/merico-dev/lake/models/domainlayer"
	"github.com/merico-dev/lake/models/domainlayer/didgen"
	"github.com/merico-dev/lake/models/domainlayer/user"
	jiraModels "github.com/merico-dev/lake/plugins/jira/models"
	"gorm.io/gorm/clause"
)

func ConvertUsers(sourceId uint64) error {

	var jiraUserRows []*jiraModels.JiraUser

	err := lakeModels.Db.Find(&jiraUserRows, "source_id = ?", sourceId).Error
	if err != nil {
		return err
	}

	userIdGen := didgen.NewDomainIdGenerator(&jiraModels.JiraUser{})

	for _, jiraUser := range jiraUserRows {
		u := &user.User{
			DomainEntity: domainlayer.DomainEntity{
				Id: userIdGen.Generate(jiraUser.SourceId, jiraUser.AccountId),
			},
			Name:      jiraUser.Name,
			Email:     jiraUser.Email,
			AvatarUrl: jiraUser.AvatarUrl,
			Timezone:  jiraUser.Timezone,
		}

		err = lakeModels.Db.Clauses(clause.OnConflict{UpdateAll: true}).Create(u).Error
		if err != nil {
			return err
		}

	}
	return nil
}

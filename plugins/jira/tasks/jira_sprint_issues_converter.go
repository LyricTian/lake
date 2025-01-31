package tasks

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/merico-dev/lake/logger"
	lakeModels "github.com/merico-dev/lake/models"
	"github.com/merico-dev/lake/models/domainlayer/didgen"
	"github.com/merico-dev/lake/models/domainlayer/ticket"
	"github.com/merico-dev/lake/plugins/jira/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SprintIssuesConverter struct {
	sprintIdGen    *didgen.DomainIdGenerator
	issueIdGen     *didgen.DomainIdGenerator
	sprints        map[string]*models.JiraSprint
	sprintIssue    map[string]*ticket.SprintIssue
	status         map[string]*ticket.IssueStatusHistory
	assignee       map[string]*ticket.IssueAssigneeHistory
	sprintsHistory map[string]*ticket.IssueSprintsHistory
}

func NewSprintIssueConverter() *SprintIssuesConverter {
	return &SprintIssuesConverter{
		sprintIdGen:    didgen.NewDomainIdGenerator(&models.JiraSprint{}),
		issueIdGen:     didgen.NewDomainIdGenerator(&models.JiraIssue{}),
		sprints:        make(map[string]*models.JiraSprint),
		sprintIssue:    make(map[string]*ticket.SprintIssue),
		status:         make(map[string]*ticket.IssueStatusHistory),
		assignee:       make(map[string]*ticket.IssueAssigneeHistory),
		sprintsHistory: make(map[string]*ticket.IssueSprintsHistory),
	}
}

func (c *SprintIssuesConverter) FeedIn(sourceId uint64, cl ChangelogItemResult) {
	if cl.Field == "status" {
		err := c.handleStatus(sourceId, cl)
		if err != nil {
			return
		}
	}
	if cl.Field == "assignee" {
		err := c.handleAssignee(sourceId, cl)
		if err != nil {
			return
		}
	}
	if cl.Field != "Sprint" {
		return
	}
	from, to, err := c.parseFromTo(cl.From, cl.To)
	if err != nil {
		return
	}
	for sprintId := range from {
		err = c.handleFrom(sourceId, sprintId, cl)
		if err != nil {
			logger.Error("handle from error:", err)
			return
		}
	}
	for sprintId := range to {
		err = c.handleTo(sourceId, sprintId, cl)
		if err != nil {
			logger.Error("handle to error:", err)
			return
		}
	}
}

func (c *SprintIssuesConverter) UpdateSprintIssue() error {
	var err error
	var flag bool
	var list []*ticket.SprintIssue
	for _, fresh := range c.sprintIssue {
		var old ticket.SprintIssue
		err = lakeModels.Db.First(&old, "sprint_id = ? AND issue_id = ?", fresh.SprintId, fresh.IssueId).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			logger.Error("UpdateSprintIssue error:", err)
			return err
		}

		if old.AddedDate == nil && fresh.AddedDate != nil || old.RemovedDate == nil && fresh.RemovedDate != nil {
			flag = true
		}
		if old.AddedDate != nil && fresh.AddedDate != nil && old.AddedDate.Before(*fresh.AddedDate) {
			fresh.AddedDate = old.AddedDate
			flag = true
		}
		if old.RemovedDate != nil && fresh.RemovedDate != nil && old.RemovedDate.After(*fresh.RemovedDate) {
			fresh.RemovedDate = old.RemovedDate
			flag = true
		}
		if fresh.AddedDate != nil && fresh.RemovedDate != nil {
			fresh.IsRemoved = fresh.AddedDate.Before(*fresh.RemovedDate)
			if fresh.IsRemoved != old.IsRemoved {
				flag = true
			}
		}
		if flag {
			list = append(list, fresh)
		}
	}
	return lakeModels.Db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).CreateInBatches(list, BatchSize).Error
}

func (c *SprintIssuesConverter) parseFromTo(from, to string) (map[uint64]struct{}, map[uint64]struct{}, error) {
	fromInts := make(map[uint64]struct{})
	toInts := make(map[uint64]struct{})
	var n uint64
	var err error
	for _, item := range strings.Split(from, ",") {
		s := strings.TrimSpace(item)
		if s == "" {
			continue
		}
		n, err = strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, nil, err
		}
		fromInts[n] = struct{}{}
	}
	for _, item := range strings.Split(to, ",") {
		s := strings.TrimSpace(item)
		if s == "" {
			continue
		}
		n, err = strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, nil, err
		}
		toInts[n] = struct{}{}
	}
	inter := make(map[uint64]struct{})
	for k := range fromInts {
		if _, ok := toInts[k]; ok {
			inter[k] = struct{}{}
			delete(toInts, k)
		}
	}
	for k := range inter {
		delete(fromInts, k)
	}
	return fromInts, toInts, nil
}

func (c *SprintIssuesConverter) handleFrom(sourceId, sprintId uint64, cl ChangelogItemResult) error {
	domainSprintId := c.sprintIdGen.Generate(sourceId, sprintId)
	key := fmt.Sprintf("%d:%d:%d", sourceId, sprintId, cl.IssueId)
	if item, ok := c.sprintIssue[key]; ok {
		if item != nil && (item.RemovedDate == nil || item.RemovedDate != nil && item.RemovedDate.Before(cl.Created)) {
			item.RemovedDate = &cl.Created
		}
	} else {
		c.sprintIssue[key] = &ticket.SprintIssue{
			SprintId:    domainSprintId,
			IssueId:     c.issueIdGen.Generate(sourceId, cl.IssueId),
			AddedDate:   nil,
			RemovedDate: &cl.Created,
		}
	}
	k := fmt.Sprintf("%d:%d", sprintId, cl.IssueId)
	if item := c.sprintsHistory[k]; item != nil {
		item.EndDate = &cl.Created
		err := lakeModels.Db.Create(item).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *SprintIssuesConverter) handleTo(sourceId, sprintId uint64, cl ChangelogItemResult) error {
	domainSprintId := c.sprintIdGen.Generate(sourceId, sprintId)
	key := fmt.Sprintf("%d:%d:%d", sourceId, sprintId, cl.IssueId)
	if item, ok := c.sprintIssue[key]; ok {
		if item != nil && (item.AddedDate == nil || item.AddedDate != nil && item.AddedDate.After(cl.Created)) {
			item.AddedDate = &cl.Created
			item.AddedStage, _ = c.getStage(cl.Created, sourceId, sprintId)
		}
	} else {
		c.sprintIssue[key] = &ticket.SprintIssue{
			SprintId:    domainSprintId,
			IssueId:     c.issueIdGen.Generate(sourceId, cl.IssueId),
			AddedDate:   &cl.Created,
			RemovedDate: nil,
		}
		c.sprintIssue[key].AddedStage, _ = c.getStage(cl.Created, sourceId, sprintId)
	}
	k := fmt.Sprintf("%d:%d", sprintId, cl.IssueId)
	c.sprintsHistory[k] = &ticket.IssueSprintsHistory{
		IssueId:   c.issueIdGen.Generate(sourceId, cl.IssueId),
		SprintId:  domainSprintId,
		StartDate: cl.Created,
		EndDate:   nil,
	}
	return nil
}

func (c *SprintIssuesConverter) getSprint(sourceId, sprintId uint64) (*models.JiraSprint, error) {
	id := c.sprintIdGen.Generate(sourceId, sprintId)
	if value, ok := c.sprints[id]; ok {
		return value, nil
	}
	var sprint models.JiraSprint
	err := lakeModels.Db.First(&sprint, "source_id = ? AND sprint_id = ?", sourceId, sprintId).Error
	if err != nil {
		c.sprints[id] = &sprint
	}
	return &sprint, err
}

func (c *SprintIssuesConverter) getStage(t time.Time, sourceId, sprintId uint64) (string, error) {
	sprint, err := c.getSprint(sourceId, sprintId)
	if err != nil {
		return "", err
	}
	if sprint.StartDate != nil {
		if sprint.StartDate.After(t) {
			return ticket.BeforeSprint, nil
		}
		if sprint.StartDate.Equal(t) || (sprint.CompleteDate != nil && sprint.CompleteDate.Equal(t)) {
			return ticket.DuringSprint, nil
		}
		if sprint.CompleteDate != nil && sprint.StartDate.Before(t) && sprint.CompleteDate.After(t) {
			return ticket.DuringSprint, nil
		}
	}
	if sprint.CompleteDate != nil && sprint.CompleteDate.Before(t) {
		return ticket.AfterSprint, nil
	}
	return "", nil
}

func (c *SprintIssuesConverter) handleStatus(sourceId uint64, cl ChangelogItemResult) error {
	issueId := c.issueIdGen.Generate(sourceId, cl.IssueId)
	if statusHistory := c.status[issueId]; statusHistory != nil {
		statusHistory.EndDate = &cl.Created
		err := lakeModels.Db.Create(statusHistory).Error
		if err != nil {
			return err
		}
	}
	c.status[issueId] = &ticket.IssueStatusHistory{
		IssueId:   issueId,
		Status:    cl.ToString,
		StartDate: cl.Created,
		EndDate:   nil,
	}
	return nil
}

func (c *SprintIssuesConverter) handleAssignee(sourceId uint64, cl ChangelogItemResult) error {
	issueId := c.issueIdGen.Generate(sourceId, cl.IssueId)
	if assigneeHistory := c.assignee[issueId]; assigneeHistory != nil {
		assigneeHistory.EndDate = &cl.Created
		err := lakeModels.Db.Create(assigneeHistory).Error
		if err != nil {
			return err
		}
	}
	c.assignee[issueId] = &ticket.IssueAssigneeHistory{
		IssueId:   issueId,
		Assignee:  cl.To,
		StartDate: cl.Created,
		EndDate:   nil,
	}
	return nil
}

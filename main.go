package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/google/go-github/v45/github"
	"github.com/sethvargo/go-githubactions"
	"golang.org/x/oauth2"
)

const (
	MessageLabelMissing = `Please provide a correct documentation label for your PR.
Instructions see [Pulsar Documentation Label Guide](https://docs.google.com/document/d/1Qw7LHQdXWBW9t2-r-A7QdFDBwmZh6ytB4guwMoXHqc0).`
	MessageLabelMultiple = `Please select only one documentation label for your PR.
Instructions see [Pulsar Documentation Label Guide](https://docs.google.com/document/d/1Qw7LHQdXWBW9t2-r-A7QdFDBwmZh6ytB4guwMoXHqc0).`
)

type ActionConfig struct {
	token  *string
	repo   *string
	owner  *string
	number *int

	labelPattern        *string
	labelWatchSet       map[string]struct{}
	labelMissing        *string
	enableLabelMissing  *bool
	enableLabelMultiple *bool

	// labels extracted from PR body
	labels map[string]bool
}

func NewActionConfig() (*ActionConfig, error) {
	ownerRepoSlug := os.Getenv("GITHUB_REPOSITORY")
	ownerRepo := strings.Split(ownerRepoSlug, "/")
	if len(ownerRepo) != 2 {
		return nil, fmt.Errorf("GITHUB_REPOSITORY is not found")
	}
	owner, repo := ownerRepo[0], ownerRepo[1]

	token := os.Getenv("GITHUB_TOKEN")

	labelPattern := os.Getenv("LABEL_PATTERN")
	if len(labelPattern) == 0 {
		labelPattern = "- \\[(.*?)\\] ?`(.+?)`"
	}

	labelWatchListSlug := os.Getenv("LABEL_WATCH_LIST")
	labelWatchList := strings.Split(strings.TrimSpace(labelWatchListSlug), ",")
	labelWatchSet := make(map[string]struct{})
	for _, l := range labelWatchList {
		labelWatchSet[l] = struct{}{}
	}

	enableLabelMissingSlug := os.Getenv("ENABLE_LABEL_MISSING")
	enableLabelMissing := true
	if enableLabelMissingSlug == "false" {
		enableLabelMissing = false
	}

	labelMissing := os.Getenv("LABEL_MISSING")
	if len(labelMissing) == 0 {
		labelMissing = "label-missing"
	}

	enableLabelMultipleSlug := os.Getenv("ENABLE_LABEL_MULTIPLE")
	enableLabelMultiple := false
	if enableLabelMultipleSlug == "true" {
		enableLabelMultiple = true
	}

	return &ActionConfig{
		token:               &token,
		repo:                &repo,
		owner:               &owner,
		labelPattern:        &labelPattern,
		labelWatchSet:       labelWatchSet,
		labelMissing:        &labelMissing,
		enableLabelMissing:  &enableLabelMissing,
		enableLabelMultiple: &enableLabelMultiple,
	}, nil
}

func (ac *ActionConfig) GetToken() string {
	if ac == nil || ac.token == nil {
		return ""
	}
	return *ac.token
}

func (ac *ActionConfig) GetOwner() string {
	if ac == nil || ac.owner == nil {
		return ""
	}
	return *ac.owner
}

func (ac *ActionConfig) GetRepo() string {
	if ac == nil || ac.repo == nil {
		return ""
	}
	return *ac.repo
}

func (ac *ActionConfig) GetNumber() int {
	if ac == nil || ac.number == nil {
		return 0
	}
	return *ac.number
}

func (ac *ActionConfig) GetLabelPattern() string {
	if ac == nil || ac.labelPattern == nil {
		return ""
	}
	return *ac.labelPattern
}

func (ac *ActionConfig) GetLabelMissing() string {
	if ac == nil || ac.labelMissing == nil {
		return ""
	}
	return *ac.labelMissing
}

func (ac *ActionConfig) GetEnableLabelMissing() bool {
	if ac == nil || ac.enableLabelMissing == nil {
		return false
	}
	return *ac.enableLabelMissing
}

func (ac *ActionConfig) GetEnableLabelMultiple() bool {
	if ac == nil || ac.enableLabelMultiple == nil {
		return false
	}
	return *ac.enableLabelMultiple
}

type Action struct {
	config *ActionConfig

	globalContext context.Context
	client        *github.Client

	// opened, edited, labeled, unlabeled
	event string
}

func NewAction(ac *ActionConfig) *Action {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: ac.GetToken()},
	)

	tc := oauth2.NewClient(ctx, ts)

	return &Action{
		config:        ac,
		globalContext: ctx,
		client:        github.NewClient(tc),
	}
}

func (a *Action) Run(actionType string) error {
	a.event = actionType
	switch actionType {
	case "opened", "edited":
		return a.OnPullRequestOpenedOrEdited()
	case "labeled", "unlabeled":
		return a.OnPullRequestLabeledOrUnlabeled()
	}
	return nil
}

func (a *Action) OnPullRequestOpenedOrEdited() error {
	pr, _, err := a.client.PullRequests.Get(a.globalContext, a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber())
	if err != nil {
		return fmt.Errorf("get PR: %v", err)
	}

	// Get repo labels
	log.Println("@List repo labels")
	repoLabels, err := a.getRepoLabels()
	if err != nil {
		log.Fatalln("List repo labels: ", err)
	}
	log.Printf("Repo labels: %v\n", a.labelsToString(repoLabels))

	repoLabelsSet := make(map[string]struct{})
	for _, label := range repoLabels {
		repoLabelsSet[label.GetName()] = struct{}{}
	}

	// Get current labels on this PR
	log.Println("@List issue labels")
	issueLabels, err := a.getIssueLabels()
	if err != nil {
		log.Fatalln("List current issue labels: ", err)
	}
	log.Printf("Issue labels: %v\n", a.labelsToString(issueLabels))

	// Get the intersection of issueLabels and labelWatchSet, including labelMissing
	log.Println("@List current labels")
	currentLabelsSet := make(map[string]struct{})
	for _, label := range issueLabels {
		if _, exist := a.config.labelWatchSet[label.GetName()]; !exist && label.GetName() != a.config.GetLabelMissing() {
			continue
		}
		currentLabelsSet[label.GetName()] = struct{}{}
	}
	log.Printf("Current labels: %v\n", a.labelsSetToString(currentLabelsSet))

	// Get expected labels
	// Only handle labels already exist in repo
	log.Println("@List expected labels")
	expectedLabelsMap := make(map[string]bool)
	for label, checked := range a.config.labels {
		if _, exist := repoLabelsSet[label]; !exist {
			log.Printf("Found label %v not exist int repo\n", label)
			continue
		}
		expectedLabelsMap[label] = checked
	}
	log.Printf("Expected labels: %v\n", expectedLabelsMap)

	// Remove labels
	log.Println("@Remove labels")
	labelsToRemove := make(map[string]struct{})
	if len(expectedLabelsMap) == 0 { // Remove current labels when PR body is empty
		for l := range a.config.labelWatchSet {
			if _, exist := currentLabelsSet[l]; exist {
				labelsToRemove[l] = struct{}{}
			}
		}
	} else {
		for label := range currentLabelsSet {
			if label == a.config.GetLabelMissing() {
				continue
			}
			if checked, exist := expectedLabelsMap[label]; exist && checked {
				continue
			}
			labelsToRemove[label] = struct{}{}
		}
	}

	// Remove missing label
	checkedCount := 0
	for _, checked := range expectedLabelsMap {
		if checked {
			checkedCount++
		}
	}

	if !a.config.GetEnableLabelMultiple() && checkedCount > 1 {
		log.Println("Multiple labels detected")
		_, _, err = a.client.Issues.CreateComment(a.globalContext,
			a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(),
			&github.IssueComment{
				Body: func(v string) *string { return &v }(fmt.Sprintf("@%s %s", pr.User.GetLogin(), MessageLabelMultiple))})
		if err != nil {
			return fmt.Errorf("create issue comment: %v", err)
		}
		return fmt.Errorf("%s", MessageLabelMultiple)
	}

	if _, exist := currentLabelsSet[a.config.GetLabelMissing()]; exist && checkedCount > 0 {
		labelsToRemove[a.config.GetLabelMissing()] = struct{}{}
	}

	log.Printf("Labels to remove: %v\n", a.labelsSetToString(labelsToRemove))

	for label := range labelsToRemove {
		_, err := a.client.Issues.RemoveLabelForIssue(a.globalContext, a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(), label)
		if err != nil {
			return fmt.Errorf("remove label %v: %v", label, err)
		}
	}

	// Add labels
	log.Println("@Add labels")

	labelsToAdd := []string{}
	for label, checked := range expectedLabelsMap {
		if !checked {
			continue
		}
		if _, exist := currentLabelsSet[label]; !exist {
			labelsToAdd = append(labelsToAdd, label)
		}
	}

	if len(labelsToAdd) == 0 {
		log.Println("No labels to add.")
	} else {
		log.Printf("Labels to add: %v\n", labelsToAdd)

		_, _, err = a.client.Issues.AddLabelsToIssue(a.globalContext, a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(), labelsToAdd)
		if err != nil {
			log.Printf("Add labels %v: %v\n", labelsToAdd, err)
		}
	}

	// Add missing label
	if a.config.GetEnableLabelMissing() && checkedCount == 0 {
		log.Println("@Add missing label")
		_, _, err = a.client.Issues.AddLabelsToIssue(a.globalContext,
			a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(),
			[]string{a.config.GetLabelMissing()})
		if err != nil {
			return fmt.Errorf("add missing label %v: %v", a.config.GetLabelMissing(), err)
		}

		_, _, err = a.client.Issues.CreateComment(a.globalContext,
			a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(),
			&github.IssueComment{
				Body: func(v string) *string { return &v }(fmt.Sprintf("@%s %s", pr.User.GetLogin(), MessageLabelMissing))})
		if err != nil {
			log.Printf("Create issue comment: %v\n", err)
		}

		return fmt.Errorf("%s", MessageLabelMissing)
	}

	return nil
}

func (a *Action) OnPullRequestLabeledOrUnlabeled() error {
	pr, _, err := a.client.PullRequests.Get(a.globalContext, a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber())
	if err != nil {
		return fmt.Errorf("get PR: %v", err)
	}

	// Get repo labels
	log.Println("@List repo labels")
	repoLabels, err := a.getRepoLabels()
	if err != nil {
		log.Fatalln("List repo labels: ", err)
	}
	log.Printf("Repo labels: %v\n", a.labelsToString(repoLabels))

	repoLabelsSet := make(map[string]struct{})
	for _, label := range repoLabels {
		repoLabelsSet[label.GetName()] = struct{}{}
	}

	// Get current labels on this PR
	log.Println("@List issue labels")
	issueLabels, err := a.getIssueLabels()
	if err != nil {
		log.Fatalln("List current issue labels: ", err)
	}
	log.Printf("Issue labels: %v\n", a.labelsToString(issueLabels))

	// Get the intersection of issueLabels and labelWatchSet, including labelMissing
	log.Println("@List current labels")
	currentLabelsSet := make(map[string]struct{})
	for _, label := range issueLabels {
		if _, exist := a.config.labelWatchSet[label.GetName()]; !exist && label.GetName() != a.config.GetLabelMissing() {
			continue
		}
		currentLabelsSet[label.GetName()] = struct{}{}
	}
	log.Printf("Current labels: %v\n", a.labelsSetToString(currentLabelsSet))

	// Get expected labels
	// Only handle labels already exist in repo
	log.Println("@List expected labels")
	expectedLabelsMap := make(map[string]bool)
	for label, checked := range a.config.labels {
		if _, exist := repoLabelsSet[label]; !exist {
			log.Printf("Found label %v not exist int repo\n", label)
			continue
		}
		expectedLabelsMap[label] = checked
	}
	log.Printf("Expected labels: %v\n", expectedLabelsMap)

	// Remove missing label
	labelsToRemove := []string{}
	checkedCount := 0
	for label := range currentLabelsSet {
		if _, exist := expectedLabelsMap[label]; !exist && label != a.config.GetLabelMissing() {
			checkedCount++
		}
	}

	if !a.config.GetEnableLabelMultiple() && checkedCount > 1 {
		log.Println("Multiple labels detected")
		_, _, err = a.client.Issues.CreateComment(a.globalContext,
			a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(),
			&github.IssueComment{
				Body: func(v string) *string { return &v }(fmt.Sprintf("@%s %s", pr.User.GetLogin(), MessageLabelMultiple))})
		if err != nil {
			return fmt.Errorf("create issue comment: %v", err)
		}
		return fmt.Errorf("%s", MessageLabelMultiple)
	}

	if _, exist := currentLabelsSet[a.config.GetLabelMissing()]; exist && checkedCount > 0 {
		labelsToRemove = append(labelsToRemove, a.config.GetLabelMissing())
	}

	log.Printf("Labels to remove: %v\n", labelsToRemove)

	for _, label := range labelsToRemove {
		_, err := a.client.Issues.RemoveLabelForIssue(a.globalContext, a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(), label)
		if err != nil {
			return fmt.Errorf("remove label %v: %v", label, err)
		}
	}

	// Add missing label
	if a.config.GetEnableLabelMissing() && checkedCount == 0 {
		log.Println("@Add missing label")
		_, _, err = a.client.Issues.AddLabelsToIssue(a.globalContext,
			a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(),
			[]string{a.config.GetLabelMissing()})
		if err != nil {
			return fmt.Errorf("add missing label %v: %v", a.config.GetLabelMissing(), err)
		}

		_, _, err = a.client.Issues.CreateComment(a.globalContext,
			a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(),
			&github.IssueComment{
				Body: func(v string) *string { return &v }(fmt.Sprintf("@%s %s", pr.User.GetLogin(), MessageLabelMissing))})
		if err != nil {
			log.Printf("Create issue comment: %v\n", err)
		}

		return fmt.Errorf("%s", MessageLabelMissing)
	}

	// Update PR Body
	// Compare current labels and expected labels
	if a.event == "unlabeled" {
		return nil
	}

	changeList := make(map[string]bool)
	for label := range currentLabelsSet {
		if checked, exist := expectedLabelsMap[label]; exist && checked {
			continue
		}

		// If not exist, need to add

		// If exist but not checked, need to update

		changeList[label] = true
	}

	for label, checked := range expectedLabelsMap {
		if _, exist := currentLabelsSet[label]; !exist && checked {
			changeList[label] = false
		}
	}

	body := pr.GetBody()
	for label, checked := range changeList {
		src := fmt.Sprintf("- [ ] `%s`", label)
		dst := fmt.Sprintf("- [x] `%s`", label)
		if !checked {
			src = fmt.Sprintf("- [x] `%s`", label)
			dst = fmt.Sprintf("- [ ] `%s`", label)
		}

		if strings.Contains(body, src) { // Update the label
			body = strings.Replace(body, src, dst, 1)
		} else { // Add the label
			body = fmt.Sprintf("%s\r\n%s\r\n", body, dst)
		}
	}

	if len(changeList) > 0 {
		log.Println("@Update PR body")
		log.Printf("ChangeList: %v\n", changeList)

		_, _, err = a.client.PullRequests.Edit(a.globalContext, a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(),
			&github.PullRequest{Body: &body})
		if err != nil {
			return fmt.Errorf("edit PR: %v", err)
		}
	}

	return nil
}

func (a *Action) extractLabels(prBody string) map[string]bool {
	r := regexp.MustCompile(a.config.GetLabelPattern())
	targets := r.FindAllStringSubmatch(prBody, -1)
	labels := make(map[string]bool)

	//// Init labels from watch list
	//for label := range a.config.labelWatchSet {
	//	labels[label] = false
	//}

	for _, v := range targets {
		checked := strings.ToLower(strings.TrimSpace(v[1])) == "x"
		name := strings.TrimSpace(v[2])

		// Filter uninterested labels
		if _, exist := a.config.labelWatchSet[name]; !exist {
			continue
		}

		labels[name] = checked
	}

	return labels
}

func (a *Action) getRepoLabels() ([]*github.Label, error) {
	ctx := context.Background()
	listOptions := &github.ListOptions{PerPage: 100}
	repoLabels := make([]*github.Label, 0)
	for {
		rLabels, resp, err := a.client.Issues.ListLabels(ctx, a.config.GetOwner(), a.config.GetRepo(), listOptions)
		if err != nil {
			return nil, err
		}
		repoLabels = append(repoLabels, rLabels...)
		if resp.NextPage == 0 {
			break
		}
		listOptions.Page = resp.NextPage
	}
	return repoLabels, nil
}

func (a *Action) getIssueLabels() ([]*github.Label, error) {
	ctx := context.Background()
	listOptions := &github.ListOptions{PerPage: 100}
	issueLabels := make([]*github.Label, 0)
	for {
		iLabels, resp, err := a.client.Issues.ListLabelsByIssue(ctx, a.config.GetOwner(), a.config.GetRepo(), a.config.GetNumber(), listOptions)
		if err != nil {
			return nil, err
		}
		issueLabels = append(issueLabels, iLabels...)
		if resp.NextPage == 0 {
			break
		}
		listOptions.Page = resp.NextPage
	}
	return issueLabels, nil
}

func (a *Action) labelsToString(labels []*github.Label) []string {
	result := []string{}
	for _, label := range labels {
		result = append(result, label.GetName())
	}
	return result
}

func (a *Action) labelsSetToString(labels map[string]struct{}) []string {
	result := []string{}
	for label := range labels {
		result = append(result, label)
	}
	return result
}

func main() {
	log.Println("@Start docbot")

	actionConfig, err := NewActionConfig()
	if err != nil {
		log.Fatalf("Get action config: %v\n", err)
	}

	action := NewAction(actionConfig)

	githubContext, err := githubactions.Context()
	if err != nil {
		log.Fatalf("Get github context: %v\n", err)
	}

	if githubContextBytes, err := json.Marshal(githubContext); err == nil {
		log.Printf("githubContext: %v\n", string(githubContextBytes))
	}

	switch githubContext.EventName {
	case "issues":
		log.Println("@EventName is issues")
	case "pull_request", "pull_request_target":
		log.Println("@EventName is PR")

		actionType, ok := githubContext.Event["action"].(string)
		if !ok {
			log.Fatalln("Action type is not string")
		}

		pr := githubContext.Event["pull_request"]
		pullRequest, ok := pr.(map[string]interface{})
		if !ok {
			log.Fatalln("PR event is not map")
		}

		number := int(githubContext.Event["number"].(float64))

		prBody, ok := pullRequest["body"].(string)
		if !ok {
			log.Fatalln("PR body is not string")
		}

		// Get expected labels
		labels := action.extractLabels(prBody)

		actionConfig.number = &number
		actionConfig.labels = labels

		if err := action.Run(actionType); err != nil {
			log.Fatalln(err)
		}
	}
}

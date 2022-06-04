package main

import (
	"context"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/google/go-github/v45/github"
	"github.com/sethvargo/go-githubactions"
	"golang.org/x/oauth2"
)

func extractLabels(prBody, labelPattern string, labelWatchMap map[string]struct{}) map[string]bool {
	r := regexp.MustCompile(labelPattern)
	targets := r.FindAllStringSubmatch(prBody, -1)

	labels := make(map[string]bool)

	for _, v := range targets {
		//log.Printf("v: %#v\n", v)
		checked := strings.ToLower(strings.TrimSpace(v[1])) == "x"
		name := strings.TrimSpace(v[2])

		// Filter uninterested labels
		if _, exist := labelWatchMap[name]; !exist {
			continue
		}

		labels[name] = checked
	}

	return labels
}

func getRepoLabels(client *github.Client, owner, repo string) ([]*github.Label, error) {
	ctx := context.Background()
	listOptions := &github.ListOptions{PerPage: 100}
	repoLabels := make([]*github.Label, 0)
	for {
		rLabels, resp, err := client.Issues.ListLabels(ctx, owner, repo, listOptions)
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

func getIssueLabels(client *github.Client, owner, repo string, number int) ([]*github.Label, error) {
	ctx := context.Background()
	listOptions := &github.ListOptions{PerPage: 100}
	issueLabels := make([]*github.Label, 0)
	for {
		iLabels, resp, err := client.Issues.ListLabelsByIssue(ctx, owner, repo, number, listOptions)
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

func run(token, owner, repo string, number int, labels map[string]bool, labelWatchMap map[string]struct{}, enableMissing bool, labelMissing string, enableLabelMultiple bool) {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)

	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Get repo labels
	log.Println("@List repo labels")
	repoLabels, err := getRepoLabels(client, owner, repo)
	if err != nil {
		log.Fatalln("List repo labels: ", err)
	}

	repoLabelsMap := make(map[string]struct{})
	for _, label := range repoLabels {
		repoLabelsMap[label.GetName()] = struct{}{}
	}
	log.Printf("Repo labels: %v\n", repoLabelsMap)

	// Get expected labels
	// Only handle labels already exist in repo
	for label := range labels {
		if _, exist := repoLabelsMap[label]; !exist {
			log.Printf("Found label %v not exist int repo\n", label)
			delete(labels, label)
		}
	}
	log.Printf("Expected labels: %v\n", labels)

	// Get current labels on this PR
	log.Println("@List current labels")
	currentLabels, err := getIssueLabels(client, owner, repo, number)
	if err != nil {
		log.Fatalln("List current issue labels: ", err)
	}

	currentLabelsMap := make(map[string]struct{})
	for _, label := range currentLabels {
		currentLabelsMap[label.GetName()] = struct{}{}
	}
	log.Printf("Current labels: %v\n", currentLabelsMap)

	// Remove labels
	log.Println("@Remove labels")

	labelsToRemove := []string{}
	if len(labels) == 0 { // Remove current labels when PR body is empty
		for l := range labelWatchMap {
			if _, exist := currentLabelsMap[l]; exist {
				labelsToRemove = append(labelsToRemove, l)
			}
		}
	} else {
		for _, label := range currentLabels {
			if checked, exist := labels[label.GetName()]; exist && !checked {
				labelsToRemove = append(labelsToRemove, label.GetName())
			}
		}
	}

	// Remove missing labels
	checkedCount := 0
	for _, checked := range labels {
		if checked {
			checkedCount++
		}
	}

	if !enableLabelMultiple && checkedCount > 1 {
		log.Println("Multiple labels detected")
		_, _, err = client.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{
			Body: func(v string) *string { return &v }("Please select only one label.")})
		if err != nil {
			log.Printf("Create issue comment: %v\n", err)
			return
		}
		return
	}

	if _, exist := currentLabelsMap[labelMissing]; exist && checkedCount > 0 {
		labelsToRemove = append(labelsToRemove, labelMissing)
	}

	log.Printf("Labels to remove: %v\n", labelsToRemove)

	for _, label := range labelsToRemove {
		_, err := client.Issues.RemoveLabelForIssue(ctx, owner, repo, number, label)
		if err != nil {
			log.Printf("Remove label %v: %v\n", label, err)
		}
	}

	// Add labels
	log.Println("@Add labels")

	labelsToAdd := []string{}
	for label, checked := range labels {
		if !checked {
			continue
		}
		if _, exist := currentLabelsMap[label]; !exist {
			labelsToAdd = append(labelsToAdd, label)
		}
	}

	if len(labelsToAdd) == 0 {
		log.Println("No labels to add.")
	} else {
		log.Printf("Labels to add: %v\n", labelsToAdd)

		_, _, err = client.Issues.AddLabelsToIssue(ctx, owner, repo, number, labelsToAdd)
		if err != nil {
			log.Printf("Add labels %v: %v\n", labelsToAdd, err)
		}
	}

	// Add missing label
	if enableMissing && checkedCount == 0 {
		log.Println("@Add missing label")
		_, _, err = client.Issues.AddLabelsToIssue(ctx, owner, repo, number, []string{labelMissing})
		if err != nil {
			log.Printf("Add missing label %v: %v\n", labelMissing, err)
		}
	}
}

func main() {
	log.Println("@Start docbot")

	ownerRepoSlug := os.Getenv("GITHUB_REPOSITORY")
	ownerRepo := strings.Split(ownerRepoSlug, "/")
	if len(ownerRepo) != 2 {
		log.Fatalln("Not found owner/repo.")
	}
	owner, repo := ownerRepo[0], ownerRepo[1]

	token := os.Getenv("GITHUB_TOKEN")

	labelPattern := os.Getenv("LABEL_PATTERN")
	if len(labelPattern) == 0 {
		labelPattern = "- \\[(.*?)\\] ?`(.+?)`"
	}

	labelWatchListSlug := os.Getenv("LABEL_WATCH_LIST")
	log.Printf("labelWatchListSlug: %v\n", labelWatchListSlug)
	labelWatchList := strings.Split(strings.TrimSpace(labelWatchListSlug), ",")

	labelWatchMap := make(map[string]struct{})
	for _, l := range labelWatchList {
		labelWatchMap[l] = struct{}{}
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

	log.Printf("owner=%v,repo=%v\n", owner, repo)
	log.Println("token=", token)
	log.Println("labelPattern=", labelPattern)
	log.Println("labelWatchList=", labelWatchList)

	githubContext, err := githubactions.Context()
	if err != nil {
		log.Fatalf("Get github context: %v\n", err)
	}

	//githubContextBytes, err := json.Marshal(githubContext)
	//if err != nil {
	//	log.Fatalf("JSON Marshal github context: ", err)
	//}
	//log.Printf("githubContext: %v\n", string(githubContextBytes))

	switch githubContext.EventName {
	case "issues":
		log.Println("@EventName is issues")
	case "pull_request", "pull_request_target":
		log.Println("@EventName is PR")

		pr := githubContext.Event["pull_request"]
		pullRequest, ok := pr.(map[string]interface{})
		if !ok {
			log.Fatalln("PR event is not map")
		}

		prBody, ok := pullRequest["body"].(string)
		if !ok {
			log.Fatalln("PR body is not string")
		}

		log.Printf("pullRequest[\"number\"]: %#v\n", pullRequest["number"])
		prNumber := int(pullRequest["number"].(float64))

		//log.Println("PR Body: ", prBody)

		// Get expected labels

		labels := extractLabels(prBody, labelPattern, labelWatchMap)
		log.Printf("labels: %#v\n", labels)

		run(token, owner, repo, prNumber, labels, labelWatchMap, enableLabelMissing, labelMissing, enableLabelMultiple)
	}
}

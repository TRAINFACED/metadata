package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-github/v58/github"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

type Payload struct {
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
}

func main() {
	var pat string
	cmd := &cobra.Command{
		Use:   "handle-metadata",
		Short: "Handles metadata dispatch from GitHub webhook",
		Run: func(cmd *cobra.Command, args []string) {
			runHandler(pat)
		},
	}

	cmd.Flags().StringVar(&pat, "token", os.Getenv("METADATA_REPO_PAT"), "GitHub token (or set METADATA_REPO_PAT env var)")
	cobra.CheckErr(cmd.Execute())
}

func runHandler(pat string) {
	ctx := context.Background()
	if pat == "" {
		die("GitHub token is required via --token or METADATA_REPO_PAT")
	}
	tokenSrc := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: pat})
	client := github.NewClient(oauth2.NewClient(ctx, tokenSrc))

	var payload Payload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		die("Failed to parse payload JSON: %v", err)
	}

	repoParts := strings.Split(payload.Repo, "/")
	if len(repoParts) != 2 {
		die("Invalid repo: must be owner/repo")
	}
	owner, repoName := repoParts[0], repoParts[1]

	tag := payload.Tag
	if !strings.HasPrefix(tag, "v") {
		die("Tag must start with 'v'")
	}
	semver := strings.TrimPrefix(tag, "v")
	parts := strings.Split(semver, ".")
	if len(parts) != 3 {
		die("Tag must be in semver format vMAJOR.MINOR.PATCH")
	}
	major, minor, patch := parts[0], parts[1], parts[2]

	validName := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !validName.MatchString(repoName) {
		die("Invalid repo name: %s", repoName)
	}

	imageURI := fmt.Sprintf("168442440833.dkr.ecr.us-west-2.amazonaws.com/baton-%s:%s.%s.%s-arm64", repoName, major, minor, patch)
	branchName := fmt.Sprintf("metadata-%s-%s", strings.ReplaceAll(payload.Repo, "/", "-"), tag)

	// Get the base branch ref (main)
	baseRef, _, err := client.Git.GetRef(context.Background(), owner, "metadata", "refs/heads/main")
	if err != nil {
		die("Failed to get main branch ref: %v", err)
	}

	// Create new branch from base
	branchRef := &github.Reference{
		Ref: github.String("refs/heads/" + branchName),
		Object: &github.GitObject{
			SHA: baseRef.Object.SHA,
		},
	}
	_, _, err = client.Git.CreateRef(ctx, owner, "metadata", branchRef)
	if err != nil {
		die("Failed to create branch: %v", err)
	}

	// Fetch config and baton_capabilities
	authHeader := fmt.Sprintf("Bearer %s", pat)
	config := fetchJSONFile(payload.Repo, tag, "config.json", authHeader)
	baton := fetchJSONFile(payload.Repo, tag, "baton_capabilities.json", authHeader)

	// Build final metadata JSON
	combined := map[string]interface{}{
		"config":             config,
		"baton_capabilities": baton,
		"image_uri":          imageURI,
	}
	contentBytes, _ := json.MarshalIndent(combined, "", "  ")
	content := string(contentBytes)
	path := fmt.Sprintf("metadata/TRAINFACED/%s/%s/%s/%s.json", repoName, major, minor, patch)
	commitMsg := fmt.Sprintf("Add metadata for [TRAINFACED]/%s@%s", repoName, tag)

	opts := &github.RepositoryContentFileOptions{
		Message: github.String(commitMsg),
		Content: []byte(content),
		Branch:  github.String(branchName),
		Committer: &github.CommitAuthor{
			Name:  github.String("trainfaced-bot"),
			Email: github.String("bot@trainfaced.com"),
			Date:  &github.Timestamp{Time: time.Now()},
		},
	}
	_, _, err = client.Repositories.CreateFile(ctx, owner, "metadata", path, opts)
	if err != nil {
		die("Failed to create metadata file: %v", err)
	}

	// Create PR
	pr, _, err := client.PullRequests.Create(ctx, owner, "metadata", &github.NewPullRequest{
		Title: github.String(commitMsg),
		Head:  github.String(branchName),
		Base:  github.String("main"),
		Body:  github.String(fmt.Sprintf("This PR contains configuration metadata for:\n- Repo: %s\n- TAG: %s", repoName, tag)),
	})
	if err != nil {
		die("Failed to create PR: %v", err)
	}
	fmt.Printf("✅ PR created: %s\n", pr.GetHTMLURL())
}

func fetchJSONFile(repo, tag, file, auth string) map[string]interface{} {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repo, tag, file)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		die("Failed to fetch %s: %v", file, err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

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

	"github.com/Masterminds/semver"
	"github.com/google/go-github/v58/github"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

var cmd = &cobra.Command{
	Use:   "handle-on-tag",
	Short: "Handles metadata dispatch from GitHub webhook",
	RunE:  run,
}

func init() {
	cmd.Flags().StringP("repo", "r", "", "Repository in owner/repo format")
	cmd.Flags().StringP("tag", "t", "", "Tag to process")
	cmd.Flags().StringP("token", "k", "", "GitHub token")
	cmd.MarkFlagRequired("repo")
	cmd.MarkFlagRequired("tag")
	cmd.MarkFlagRequired("token")
}

func run(cmd *cobra.Command, args []string) error {
	repo, err := cmd.Flags().GetString("repo")
	if err != nil {
		return fmt.Errorf("Failed to get repo: %v", err)
	}
	tag, err := cmd.Flags().GetString("tag")
	if err != nil {
		return fmt.Errorf("Failed to get tag: %v", err)
	}
	pat, err := cmd.Flags().GetString("token")
	if err != nil {
		return fmt.Errorf("Failed to get token: %v", err)
	}
	metadataCmd := &UpdateMetadataCmd{
		repo:  repo,
		tag:   tag,
		token: pat,
	}
	return metadataCmd.Run()
}

func main() {
	err := cmd.Execute()
	if err != nil {
		fmt.Fprint(os.Stderr, err.Error())
		os.Exit(1)
	}
}

type UpdateMetadataCmd struct {
	repo  string
	tag   string
	token string
}

func (c *UpdateMetadataCmd) Run() error {
	ctx := context.Background()

	tokenSrc := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: c.token})
	client := github.NewClient(oauth2.NewClient(ctx, tokenSrc))

	repoParts := strings.Split(c.repo, "/")
	if len(repoParts) != 2 {
		return fmt.Errorf("Invalid repo: must be owner/repo")
	}
	owner, repoName := repoParts[0], repoParts[1]

	v, err := semver.NewVersion(c.tag)
	if err != nil {
		return fmt.Errorf("Failed to parse tag: %w", err)
	}

	major := v.Major()
	minor := v.Minor()
	patch := v.Patch()
	prerelease := v.Prerelease()
	semverMetadata := v.Metadata()

	effectivePatch := fmt.Sprintf("%d", patch)
	if prerelease != "" {
		effectivePatch += "-" + prerelease
	}
	if semverMetadata != "" {
		effectivePatch += "-" + semverMetadata
	}

	fmt.Printf("major: %d, minor: %d, rest: %s\n", major, minor, effectivePatch)

	validName := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !validName.MatchString(repoName) {
		return fmt.Errorf("Invalid repo name: %s", repoName)
	}

	release, _, err := client.Repositories.GetReleaseByTag(ctx, owner, repoName, c.tag)
	if err != nil {
		return fmt.Errorf("Failed to get release by tag: %v", err)
	}

	assets := []map[string]interface{}{}
	assets = append(assets, map[string]interface{}{
		"name":       "tarball",
		"url":        release.GetTarballURL(),
		"created_at": release.GetCreatedAt().Format(time.RFC3339),
	})

	assets = append(assets, map[string]interface{}{
		"name":       "zipball",
		"url":        release.GetZipballURL(),
		"created_at": release.GetCreatedAt().Format(time.RFC3339),
	})

	for _, asset := range release.Assets {
		assetInfo := map[string]interface{}{
			"name":       asset.GetName(),
			"url":        asset.GetBrowserDownloadURL(),
			"created_at": asset.GetCreatedAt().Format(time.RFC3339),
		}
		assets = append(assets, assetInfo)
	}

	imageURI := fmt.Sprintf("168442440833.dkr.ecr.us-west-2.amazonaws.com/baton-%s:%d.%d.%s-arm64", repoName, major, minor, effectivePatch)
	branchName := fmt.Sprintf("metadata-%s-%s", strings.ReplaceAll(c.repo, "/", "-"), c.tag)

	// Get the base branch ref (main)
	baseRef, _, err := client.Git.GetRef(ctx, owner, "metadata", "refs/heads/main")
	if err != nil {
		return fmt.Errorf("Failed to get main branch ref: %v", err)
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
		return fmt.Errorf("Failed to create branch: %v", err)
	}

	// Fetch config and baton_capabilities
	config, err := c.fetchJSONFile("config.json")
	if err != nil {
		return fmt.Errorf("Failed to fetch config: %v", err)
	}
	baton, err := c.fetchJSONFile("baton_capabilities.json")
	if err != nil {
		return fmt.Errorf("Failed to fetch baton_capabilities: %v", err)
	}

	// Build final metadata JSON
	combined := map[string]interface{}{
		"config":             config,
		"baton_capabilities": baton,
		"image_uri":          imageURI,
		"assets":             assets,
		// "download_url":       tarball,
		// https://github.com/ConductorOne/baton-github/releases/download/v0.1.28/baton-github-v0.1.28-linux-amd64.tar.gz
	}

	contentBytes, _ := json.MarshalIndent(combined, "", "  ")
	content := string(contentBytes)
	path := fmt.Sprintf("metadata/TRAINFACED/%s/%d/%d/%s.json", repoName, major, minor, effectivePatch)
	commitMsg := fmt.Sprintf("Add metadata for [TRAINFACED]/%s@%s", repoName, c.tag)

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
		return fmt.Errorf("Failed to create metadata file: %v", err)
	}

	// Create PR
	pr, _, err := client.PullRequests.Create(ctx, owner, "metadata", &github.NewPullRequest{
		Title: github.String(commitMsg),
		Head:  github.String(branchName),
		Base:  github.String("main"),
		Body:  github.String(fmt.Sprintf("This PR contains configuration metadata for:\n- Repo: %s\n- TAG: %s", repoName, c.tag)),
	})
	if err != nil {
		return fmt.Errorf("Failed to create PR: %v", err)
	}
	// _, _, err = client.PullRequests.Merge(ctx, owner, "metadata", pr.GetNumber(), "Merge PR", &github.PullRequestOptions{
	// 	MergeMethod: "merge",
	// })
	// if err != nil {
	// 	return fmt.Errorf("Failed to enable auto-merge: %v", err)
	// }

	fmt.Printf("✅ PR was [merged]: %s\n", pr.GetHTMLURL())
	return nil
}

func (c *UpdateMetadataCmd) fetchJSONFile(file string) (map[string]interface{}, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", c.repo, c.tag, file)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.token))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return nil, fmt.Errorf("Failed to fetch %s: %v", file, err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&out)
	if err != nil {
		return nil, fmt.Errorf("Failed to decode %s: %v", file, err)
	}
	return out, nil
}

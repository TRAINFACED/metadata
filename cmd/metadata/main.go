package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const Org = "TRAINFACED"

type Payload struct {
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
}

func main() {
	var payload Payload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		die("Failed to parse payload JSON: %v", err)
	}

	repoParts := strings.Split(payload.Repo, "/")
	repoName := repoParts[len(repoParts)-1]
	if repoName == "" {
		die("Invalid repo name")
	}
	if repoName == "" {
		die("Invalid repo name")
	}
	validName := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !validName.MatchString(repoName) {
		die("Invalid repo name: %s", repoName)
	}

	tag := payload.Tag
	if !strings.HasPrefix(tag, "v") {
		die("Tag must start with 'v'")
	}
	semver := strings.TrimPrefix(tag, "v")
	parts := strings.SplitN(semver, ".", 3)
	if len(parts) != 3 {
		die("Tag must be in semver format vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]")
	}
	major, minor, patch := parts[0], parts[1], parts[2]

	imageURI := fmt.Sprintf("168442440833.dkr.ecr.us-west-2.amazonaws.com/baton-%s:%s.%s.%s-arm64", repoName, major, minor, patch)

	authHeader := fmt.Sprintf("Bearer %s", os.Getenv("METADATA_REPO_PAT"))
	config := fetchJSONFile(payload.Repo, tag, "config.json", authHeader)
	baton := fetchJSONFile(payload.Repo, tag, "baton_capabilities.json", authHeader)

	outDir := filepath.Join("metadata", Org, repoName, major, minor)
	os.MkdirAll(outDir, 0755)
	outFile := filepath.Join(outDir, patch+".json")
	combined := map[string]interface{}{
		"config":             config,
		"baton_capabilities": baton,
		"image_uri":          imageURI,
	}
	writeJSON(outFile, combined)

	token := os.Getenv("METADATA_REPO_PAT")
	repoURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s.git", token, os.Getenv("GITHUB_REPOSITORY"))
	run("git", "remote", "set-url", "origin", repoURL)

	branch := fmt.Sprintf("metadata-%s-%s", strings.ReplaceAll(payload.Repo, "/", "-"), tag)
	run("git", "checkout", "-b", branch)
	run("git", "add", outFile)
	msg := fmt.Sprintf("Add metadata for [%s]/%s@%s", Org, repoName, tag)
	run("git", "commit", "-m", msg)
	run("git", "push", "--set-upstream", "origin", branch)

	body := fmt.Sprintf("This PR contains configuration metadata for:\n- Repo: %s\n- TAG: %s", repoName, tag)
	prURL := runOut("gh", "pr", "create", "--title", msg, "--body", body, "--head", branch, "--base", "main")
	fmt.Println("✅ Created PR:", prURL)

	// Extract PR number
	parts = strings.Split(strings.TrimSpace(prURL), "/")
	prNumber := parts[len(parts)-1]
	apiOut := runOut("gh", "api", fmt.Sprintf("repos/%s/pulls/%s", os.Getenv("GITHUB_REPOSITORY"), prNumber))
	var pr struct {
		NodeID  string `json:"node_id"`
		HTMLURL string `json:"html_url"`
	}
	json.Unmarshal([]byte(apiOut), &pr)

	autoMergeQuery := `mutation($pullRequestId: ID!) {
  enablePullRequestAutoMerge(input: {
    pullRequestId: $pullRequestId,
    mergeMethod: SQUASH
  }) {
    pullRequest {
      number
      autoMergeRequest {
        enabledAt
      }
    }
  }
}`

	r := exec.Command("gh", "api", "graphql", "-f", "query="+autoMergeQuery, "-f", "pullRequestId="+pr.NodeID)
	r.Stderr = os.Stderr
	resp, _ := r.Output()
	outStr := string(resp)
	fmt.Println(outStr)
	if strings.Contains(outStr, "UNPROCESSABLE") {
		fmt.Println("⚠️ Auto-merge not applicable, merging manually...")
		run("gh", "pr", "merge", pr.HTMLURL, "--squash")
	} else if strings.Contains(outStr, "errors") {
		die("❌ Failed to enable auto-merge")
	} else {
		fmt.Println("✅ Auto-merge enabled")
	}
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

func writeJSON(path string, data any) {
	f, _ := os.Create(path)
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		die("Command failed: %v", err)
	}
}

func runOut(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		die("Command failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Service provides Git and worktree operations for Codex UI Review Pane.
type Service struct{}

func NewService() *Service {
	return &Service{}
}

type RepoStatus struct {
	IsGitRepo     bool   `json:"isGitRepo"`
	CurrentBranch string `json:"currentBranch"`
	GitRoot       string `json:"gitRoot"`
	HeadCommit    string `json:"headCommit"`
	IsDirty       bool   `json:"isDirty"`
}

// GetRepoStatus returns basic Git repository status for a given directory.
func (s *Service) GetRepoStatus(cwd string) (*RepoStatus, error) {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = cwd
	if err := cmd.Run(); err != nil {
		return &RepoStatus{IsGitRepo: false}, nil
	}

	rootCmd := exec.Command("git", "rev-parse", "--show-toplevel")
	rootCmd.Dir = cwd
	rootOut, _ := rootCmd.Output()
	gitRoot := strings.TrimSpace(string(rootOut))

	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = cwd
	branchOut, _ := branchCmd.Output()
	branch := strings.TrimSpace(string(branchOut))

	headCmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	headCmd.Dir = cwd
	headOut, _ := headCmd.Output()
	head := strings.TrimSpace(string(headOut))

	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = cwd
	statusOut, _ := statusCmd.Output()
	isDirty := len(bytes.TrimSpace(statusOut)) > 0

	return &RepoStatus{
		IsGitRepo:     true,
		CurrentBranch: branch,
		GitRoot:       gitRoot,
		HeadCommit:    head,
		IsDirty:       isDirty,
	}, nil
}

type BranchInfo struct {
	Name      string `json:"name"`
	IsCurrent bool   `json:"isCurrent"`
	IsRemote  bool   `json:"isRemote"`
}

// ListBranches lists local and remote branches.
func (s *Service) ListBranches(cwd string) ([]BranchInfo, error) {
	cmd := exec.Command("git", "branch", "-a")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(out), "\n")
	var branches []BranchInfo
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		isCurrent := strings.HasPrefix(l, "*")
		name := strings.TrimPrefix(trimmed, "* ")
		isRemote := strings.HasPrefix(name, "remotes/")
		branches = append(branches, BranchInfo{
			Name:      name,
			IsCurrent: isCurrent,
			IsRemote:  isRemote,
		})
	}
	return branches, nil
}

type CommitInfo struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

// ListCommits returns recent commit history on the active branch.
func (s *Service) ListCommits(cwd string, limit int) ([]CommitInfo, error) {
	if limit <= 0 {
		limit = 30
	}
	cmd := exec.Command("git", "log", fmt.Sprintf("-n%d", limit), "--pretty=format:%h%x09%an%x09%ad%x09%s")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var commits []CommitInfo
	lines := strings.Split(string(out), "\n")
	for _, l := range lines {
		parts := strings.Split(l, "\t")
		if len(parts) >= 4 {
			commits = append(commits, CommitInfo{
				Hash:    parts[0],
				Author:  parts[1],
				Date:    parts[2],
				Message: parts[3],
			})
		}
	}
	return commits, nil
}

// Checkout changes branch.
func (s *Service) Checkout(cwd string, branch string) error {
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("checkout error: %s", string(out))
	}
	return nil
}

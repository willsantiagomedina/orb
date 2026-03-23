package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Snapshot contains a point-in-time repository summary for the UI.
type Snapshot struct {
	RepoPath   string
	Branch     string
	StatusText string
	DiffLines  []string
}

// Refresh reads repository branch, status, and unified diff for a path.
func Refresh(ctx context.Context, path string) (Snapshot, error) {
	repoRoot, err := resolveRepoRoot(ctx, path)
	if err != nil {
		return Snapshot{}, err
	}

	branchName := "detached"
	if branchFromGit, branchErr := runGitCommand(ctx, repoRoot, "branch", "--show-current"); branchErr == nil {
		trimmedBranch := strings.TrimSpace(branchFromGit)
		if trimmedBranch != "" {
			branchName = trimmedBranch
		}
	}

	statusText, err := loadStatusSummary(ctx, repoRoot)
	if err != nil {
		return Snapshot{}, err
	}

	diffLines, err := loadDiffLines(ctx, repoRoot)
	if err != nil {
		return Snapshot{}, err
	}
	if len(diffLines) == 0 {
		diffLines = []string{"working tree clean"}
	}

	return Snapshot{
		RepoPath:   repoRoot,
		Branch:     branchName,
		StatusText: statusText,
		DiffLines:  diffLines,
	}, nil
}

// CreateWorktree creates a per-task git worktree and returns the absolute path.
func CreateWorktree(ctx context.Context, basePath string, taskID string) (string, error) {
	cleanID := strings.ToLower(strings.TrimSpace(taskID))
	if cleanID == "" {
		return "", errors.New("create worktree: empty task id")
	}

	repoRoot, err := resolveRepoRoot(ctx, basePath)
	if err != nil {
		return "", err
	}

	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("create worktree: git binary not found: %w", err)
	}

	worktreePath := filepath.Join(repoRoot, ".orb", "worktrees", cleanID)
	if _, err := os.Stat(worktreePath); err == nil {
		return worktreePath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("check worktree path %q: %w", worktreePath, err)
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", fmt.Errorf("create worktree parent directory: %w", err)
	}

	branchName := "orb/task-" + cleanID
	branchExists, err := localBranchExists(ctx, repoRoot, branchName)
	if err != nil {
		return "", err
	}

	args := []string{"-C", repoRoot, "worktree", "add"}
	if branchExists {
		args = append(args, worktreePath, branchName)
	} else {
		args = append(args, "-b", branchName, worktreePath)
	}

	command := exec.CommandContext(ctx, "git", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		trimmedOutput := strings.TrimSpace(string(output))
		if trimmedOutput == "" {
			return "", fmt.Errorf("create worktree %q: %w", worktreePath, err)
		}
		return "", fmt.Errorf("create worktree %q: %w: %s", worktreePath, err, trimmedOutput)
	}

	return worktreePath, nil
}

func loadStatusSummary(ctx context.Context, repoRoot string) (string, error) {
	porcelain, err := runGitCommand(ctx, repoRoot, "status", "--porcelain=v1")
	if err != nil {
		return "", err
	}

	stagedCount := 0
	changedCount := 0
	untrackedCount := 0

	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 2 {
			continue
		}

		x := line[0]
		y := line[1]

		if x == '?' && y == '?' {
			untrackedCount++
			continue
		}
		if x == '!' && y == '!' {
			continue
		}
		if x != ' ' {
			stagedCount++
		}
		if y != ' ' {
			if y == '?' {
				untrackedCount++
			} else {
				changedCount++
			}
		}
	}

	return fmt.Sprintf("%d staged · %d changed · %d untracked", stagedCount, changedCount, untrackedCount), nil
}

func localBranchExists(ctx context.Context, repoPath string, branchName string) (bool, error) {
	command := exec.CommandContext(
		ctx,
		"git",
		"-C", repoPath,
		"show-ref",
		"--verify",
		"--quiet",
		"refs/heads/"+branchName,
	)
	err := command.Run()
	if err == nil {
		return true, nil
	}
	if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check branch %q: %w", branchName, err)
}

func loadDiffLines(ctx context.Context, repoRoot string) ([]string, error) {
	staged, err := runGitCommand(ctx, repoRoot, "diff", "--no-color", "--cached")
	if err != nil {
		return nil, err
	}
	unstaged, err := runGitCommand(ctx, repoRoot, "diff", "--no-color")
	if err != nil {
		return nil, err
	}

	chunks := make([]string, 0, 2)
	if strings.TrimSpace(staged) != "" {
		chunks = append(chunks, staged)
	}
	if strings.TrimSpace(unstaged) != "" {
		chunks = append(chunks, unstaged)
	}
	if len(chunks) == 0 {
		return nil, nil
	}

	all := strings.Join(chunks, "\n")
	all = strings.TrimRight(all, "\n")
	if all == "" {
		return nil, nil
	}
	return strings.Split(all, "\n"), nil
}

func runGitCommand(ctx context.Context, repoRoot string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", repoRoot}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		trimmedOutput := strings.TrimSpace(string(output))
		if trimmedOutput == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, trimmedOutput)
	}

	return strings.TrimRight(string(bytes.TrimRight(output, "\x00")), "\n"), nil
}

func resolveRepoRoot(ctx context.Context, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("resolve repository root: empty path")
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}

	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", fmt.Errorf("stat path %q: %w", absolutePath, err)
	}

	referencePath := absolutePath
	if !info.IsDir() {
		referencePath = filepath.Dir(absolutePath)
	}

	repoRoot, err := runGitCommand(ctx, referencePath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("no git repository found from %q", absolutePath)
	}
	return strings.TrimSpace(repoRoot), nil
}

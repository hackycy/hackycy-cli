package rm

import (
	"os"
	"path/filepath"
	"sync"
)

const (
	smartActionNodeDist        = "node-dist"
	smartActionNodeModules     = "node-node_modules"
	smartActionMonorepoDist    = "monorepo-dist"
	smartActionMonorepoModules = "monorepo-node_modules"
	smartActionNodeLockfile    = "node-lockfile"
	smartActionAIAgent         = "ai-agent"
)

// SmartAction is one legacy smart-cleanup action shown by the terminal adapter.
type SmartAction struct {
	ID    string
	Label string
}

var smartActions = []SmartAction{
	{ID: smartActionNodeDist, Label: "Node project - delete ./dist"},
	{ID: smartActionNodeModules, Label: "Node project - delete ./node_modules"},
	{ID: smartActionMonorepoDist, Label: "Monorepo - delete all dist dirs (recursive)"},
	{ID: smartActionMonorepoModules, Label: "Monorepo - delete all node_modules dirs (recursive)"},
	{ID: smartActionNodeLockfile, Label: "Node project - delete lockfile(s)"},
	{ID: smartActionAIAgent, Label: "AI agent config dirs (.claude, .cursor, .copilot...)"},
}

var lockfileNames = []string{
	"package-lock.json",
	"yarn.lock",
	"pnpm-lock.yaml",
	"b" + "un.lock",
	"b" + "un.lockb",
}

var aiAgentDirectoryNames = []string{
	".claude",
	".agents",
	".cursor",
	".copilot",
	".windsurf",
	".aider",
}

var skippedSmartDirectories = map[string]bool{
	".git":        true,
	".svn":        true,
	".hg":         true,
	"__pycache__": true,
}

func discoverSmart(workingDirectory string, action SmartAction, depth int) []string {
	switch action.ID {
	case smartActionNodeDist:
		return discoverNamedRootPaths(workingDirectory, []string{"dist"})
	case smartActionNodeModules:
		return discoverNamedRootPaths(workingDirectory, []string{"node_modules"})
	case smartActionMonorepoDist:
		return findDirectoriesByName(workingDirectory, "dist", depth, 0)
	case smartActionMonorepoModules:
		return findDirectoriesByName(workingDirectory, "node_modules", depth, 0)
	case smartActionNodeLockfile:
		return discoverNamedRootPaths(workingDirectory, lockfileNames)
	case smartActionAIAgent:
		return discoverNamedRootPaths(workingDirectory, aiAgentDirectoryNames)
	default:
		return []string{}
	}
}

func discoverNamedRootPaths(workingDirectory string, names []string) []string {
	targets := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(workingDirectory, name)
		if _, err := os.Stat(path); err == nil {
			targets = append(targets, path)
		}
	}
	return targets
}

func findDirectoriesByName(directory, targetName string, maxDepth, currentDepth int) []string {
	if currentDepth > maxDepth {
		return []string{}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return []string{}
	}

	results := make(chan string)
	var workers sync.WaitGroup
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entry := entry
		workers.Add(1)
		go func() {
			defer workers.Done()
			path := filepath.Join(directory, entry.Name())
			if entry.Name() == targetName {
				results <- path
				return
			}
			if skippedSmartDirectories[entry.Name()] || entry.Name()[0] == '.' {
				return
			}
			for _, nested := range findDirectoriesByName(path, targetName, maxDepth, currentDepth+1) {
				results <- nested
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()

	targets := []string{}
	for path := range results {
		targets = append(targets, path)
	}
	return targets
}

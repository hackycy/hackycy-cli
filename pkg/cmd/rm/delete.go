package rm

import "sync"

// PathRemover removes one path recursively and forcefully.
type PathRemover interface {
	RemovePath(string) error
}

type deletionResult struct {
	succeeded int
	failures  []error
}

func deletePaths(targets []string, remover PathRemover) deletionResult {
	results := make([]error, len(targets))
	var workers sync.WaitGroup
	for index, target := range targets {
		index, target := index, target
		workers.Add(1)
		go func() {
			defer workers.Done()
			results[index] = remover.RemovePath(target)
		}()
	}
	workers.Wait()

	result := deletionResult{failures: []error{}}
	for _, err := range results {
		if err != nil {
			result.failures = append(result.failures, err)
			continue
		}
		result.succeeded++
	}
	return result
}

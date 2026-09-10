package appconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

func (store *Store) writeDocument(document document) (err error) {
	lock, err := store.acquireConfigLock()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.release(); releaseErr != nil && err == nil {
			err = releaseErr
		}
	}()
	return store.publishDocument(document)
}

func (store *Store) publishDocument(document document) (err error) {
	candidateID, err := store.newLockID()
	if err != nil {
		return fmt.Errorf("create configuration candidate ID: %w", err)
	}
	candidate := store.configPath() + ".candidate-" + candidateID
	defer func() {
		if cleanupErr := os.Remove(candidate); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) && err == nil {
			err = fmt.Errorf("clean up configuration candidate: %w", cleanupErr)
		}
	}()

	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(candidate, contents, 0o600); err != nil {
		return fmt.Errorf("write configuration candidate: %w", err)
	}
	if err := store.replaceConfigFile(candidate, store.configPath()); err != nil {
		return fmt.Errorf("replace configuration: %w", err)
	}
	return nil
}

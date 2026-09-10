package appconfig

func (store *Store) updateDocument(mutate func(*document) error) (err error) {
	lock, err := store.acquireConfigLock()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.release(); releaseErr != nil && err == nil {
			err = releaseErr
		}
	}()

	document, _, err := store.readDocument()
	if err != nil {
		return err
	}
	if err := mutate(&document); err != nil {
		return err
	}
	return store.publishDocument(document)
}

func (store *Store) ensureDocument() (document document, err error) {
	lock, err := store.acquireConfigLock()
	if err != nil {
		return document, err
	}
	defer func() {
		if releaseErr := lock.release(); releaseErr != nil && err == nil {
			err = releaseErr
		}
	}()

	document, exists, err := store.readDocument()
	if err != nil || exists {
		return document, err
	}
	if err := store.publishDocument(document); err != nil {
		return document, err
	}
	return document, nil
}

func appendOrderedName(order []string, name string) []string {
	for _, existing := range order {
		if existing == name {
			return order
		}
	}
	return append(order, name)
}

func removeOrderedName(order []string, name string) []string {
	filtered := order[:0]
	for _, existing := range order {
		if existing != name {
			filtered = append(filtered, existing)
		}
	}
	return filtered
}

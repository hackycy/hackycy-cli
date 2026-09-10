package appconfig

// ForkInput is the semantic Fork record to persist.
type ForkInput struct {
	Host   string
	Scheme string
	Type   string
	Token  string
}

// ForkInstance is a secret-safe Fork projection for list presentation.
type ForkInstance struct {
	Name         string
	Host         string
	Scheme       string
	Type         string
	TokenPreview string
}

// ForkCredentials is the semantic Fork record for consumers that need its token.
type ForkCredentials struct {
	Name   string
	Host   string
	Scheme string
	Type   string
	Token  string
}

// ListForkInstances returns records in their persisted iteration order without exposing secrets.
func (store *Store) ListForkInstances() ([]ForkInstance, error) {
	document, _, err := store.readDocument()
	if err != nil {
		return nil, err
	}
	instances := make([]ForkInstance, 0, len(document.Fork.Instances))
	for _, name := range normalizedOrder(document.Fork.order, document.Fork.Instances) {
		instance := document.Fork.Instances[name]
		instances = append(instances, ForkInstance{
			Name:         name,
			Host:         instance.Host,
			Scheme:       forkScheme(instance.Scheme),
			Type:         instance.Type,
			TokenPreview: forkTokenPreview(instance.Token),
		})
	}
	return instances, nil
}

// ForkInstance returns a decrypted record by its configured name.
func (store *Store) ForkInstance(name string) (ForkCredentials, bool, error) {
	document, _, err := store.readDocument()
	if err != nil {
		return ForkCredentials{}, false, err
	}
	instance, found := document.Fork.Instances[name]
	if !found {
		return ForkCredentials{}, false, nil
	}
	credentials, err := store.decryptForkInstance(document, name, instance)
	if err != nil {
		return ForkCredentials{}, false, err
	}
	return credentials, true, nil
}

// ForkInstanceByHost returns the first persisted record with the requested host.
func (store *Store) ForkInstanceByHost(host string) (ForkCredentials, bool, error) {
	document, _, err := store.readDocument()
	if err != nil {
		return ForkCredentials{}, false, err
	}
	for _, name := range normalizedOrder(document.Fork.order, document.Fork.Instances) {
		instance := document.Fork.Instances[name]
		if instance.Host != host {
			continue
		}
		credentials, err := store.decryptForkInstance(document, name, instance)
		if err != nil {
			return ForkCredentials{}, false, err
		}
		return credentials, true, nil
	}
	return ForkCredentials{}, false, nil
}

// SaveForkInstance replaces one record while preserving all unrelated configuration fields.
func (store *Store) SaveForkInstance(name string, input ForkInput) error {
	return store.updateDocument(func(document *document) error {
		key, err := store.keyForSalt(document.Salt)
		if err != nil {
			return err
		}
		token, err := encryptValue(input.Token, key, store.random)
		if err != nil {
			return err
		}
		if document.Fork.Instances == nil {
			document.Fork.Instances = map[string]forkDocumentInstance{}
		}
		if _, exists := document.Fork.Instances[name]; !exists {
			document.Fork.order = appendOrderedName(document.Fork.order, name)
		}
		document.Fork.Instances[name] = forkDocumentInstance{
			Host:   input.Host,
			Scheme: forkScheme(input.Scheme),
			Type:   input.Type,
			Token:  token,
		}
		return nil
	})
}

// RemoveForkInstance removes one record and reports whether it existed.
func (store *Store) RemoveForkInstance(name string) (bool, error) {
	removed := false
	err := store.updateDocument(func(document *document) error {
		if _, exists := document.Fork.Instances[name]; !exists {
			return nil
		}
		delete(document.Fork.Instances, name)
		document.Fork.order = removeOrderedName(document.Fork.order, name)
		removed = true
		return nil
	})
	return removed, err
}

func (store *Store) decryptForkInstance(document document, name string, instance forkDocumentInstance) (ForkCredentials, error) {
	key, err := store.keyForSalt(document.Salt)
	if err != nil {
		return ForkCredentials{}, err
	}
	token, err := decryptValue(instance.Token, key)
	if err != nil {
		return ForkCredentials{}, err
	}
	return ForkCredentials{Name: name, Host: instance.Host, Scheme: forkScheme(instance.Scheme), Type: instance.Type, Token: token}, nil
}

func forkScheme(value string) string {
	if value == "" {
		return "https"
	}
	return value
}

func forkTokenPreview(ciphertext string) string {
	if len(ciphertext) > 4 {
		return ciphertext[:4] + "***"
	}
	return "***"
}

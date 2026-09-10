package list

import "github.com/hackycy/hackycy-cli/internal/appconfig"

// Reader supplies the secret-safe Fork projection owned by appconfig.
type Reader interface {
	ListForkInstances() ([]appconfig.ForkInstance, error)
}

// Instance is the command-owned Fork data safe for list presentation.
type Instance struct {
	Name         string
	Host         string
	Scheme       string
	Type         string
	TokenPreview string
}

// Read obtains Fork instances without decrypting or exposing tokens.
func Read(reader Reader) ([]Instance, error) {
	configured, err := reader.ListForkInstances()
	if err != nil {
		return nil, err
	}
	instances := make([]Instance, len(configured))
	for index, instance := range configured {
		instances[index] = Instance{
			Name:         instance.Name,
			Host:         instance.Host,
			Scheme:       instance.Scheme,
			Type:         instance.Type,
			TokenPreview: instance.TokenPreview,
		}
	}
	return instances, nil
}

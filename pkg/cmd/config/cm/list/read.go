package list

import "github.com/hackycy/hackycy-cli/internal/appconfig"

// Reader supplies the secret-safe CM projection owned by appconfig.
type Reader interface {
	ListCMProfiles() (appconfig.CMProfileList, error)
}

// Profile is the command-owned CM data safe for list presentation.
type Profile struct {
	Name    string
	BaseURL string
	Model   string
	Default bool
}

// Read obtains CM profiles without decrypting or exposing API keys.
func Read(reader Reader) ([]Profile, error) {
	configured, err := reader.ListCMProfiles()
	if err != nil {
		return nil, err
	}
	profiles := make([]Profile, len(configured.Profiles))
	for index, profile := range configured.Profiles {
		profiles[index] = Profile{
			Name:    profile.Name,
			BaseURL: profile.BaseURL,
			Model:   profile.Model,
			Default: profile.Name == configured.DefaultProfile,
		}
	}
	return profiles, nil
}

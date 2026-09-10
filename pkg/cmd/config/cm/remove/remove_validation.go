package remove

import (
	"fmt"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

// Reader supplies the secret-safe profile projection needed for validation.
type Reader interface {
	ListCMProfiles() (appconfig.CMProfileList, error)
}

// ValidateRemoveProfile confirms that the requested profile exists before confirmation.
func ValidateRemoveProfile(reader Reader, name string) error {
	profiles, err := reader.ListCMProfiles()
	if err != nil {
		return err
	}
	for _, profile := range profiles.Profiles {
		if profile.Name == name {
			return nil
		}
	}
	return fmt.Errorf("CM profile not found: %s", name)
}

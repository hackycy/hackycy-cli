package add

import "github.com/hackycy/hackycy-cli/internal/appconfig"

// AddWriter is the command-facing appconfig semantic mutation boundary.
type AddWriter interface {
	SaveForkInstance(name string, input appconfig.ForkInput) error
}

// SaveAdd persists one validated Fork record through appconfig.
func SaveAdd(writer AddWriter, input AddInput) error {
	if err := ValidateAddInput(input); err != nil {
		return err
	}
	return writer.SaveForkInstance(input.Alias, appconfig.ForkInput{
		Host:   input.Host,
		Scheme: input.Scheme,
		Type:   input.Type,
		Token:  input.Token,
	})
}

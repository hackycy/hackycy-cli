package add

// AddWriter is the command-facing appconfig semantic mutation boundary.
type AddWriter interface {
	AddCMProfile(name, baseURL, model, apiKey string) error
}

// SaveAdd persists one validated CM profile through appconfig.
func SaveAdd(writer AddWriter, input AddInput) error {
	if err := ValidateAddInput(input); err != nil {
		return err
	}
	return writer.AddCMProfile(input.Name, input.BaseURL, input.Model, input.APIKey)
}

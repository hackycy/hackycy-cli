package appconfig

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const (
	defaultCMTemperature     = 0.2
	defaultCMTimeoutMS       = 300000.0
	defaultCMMaxOutputTokens = 1000.0
)

// CMProfile is a secret-safe profile projection for list presentation.
type CMProfile struct {
	Name    string
	BaseURL string
	Model   string
}

// CMProfileList retains the stored default and profile iteration order.
type CMProfileList struct {
	DefaultProfile string
	Profiles       []CMProfile
}

// CMResolveOptions controls semantic profile resolution.
type CMResolveOptions struct {
	ProfileName       string
	TimeoutOverrideMS *float64
}

// ResolvedCMProfile is a decrypted profile for the provider-owning consumer.
type ResolvedCMProfile struct {
	Name            string
	BaseURL         string
	Model           string
	APIKey          string
	Temperature     float64
	TimeoutMS       float64
	MaxOutputTokens float64
}

// ListCMProfiles returns stored profile metadata without API keys.
func (store *Store) ListCMProfiles() (CMProfileList, error) {
	document, _, err := store.readDocument()
	if err != nil {
		return CMProfileList{}, err
	}
	if document.CM == nil {
		return CMProfileList{Profiles: []CMProfile{}}, nil
	}
	profiles := make([]CMProfile, 0, len(document.CM.Profiles))
	for _, name := range normalizedOrder(document.CM.order, document.CM.Profiles) {
		profile := document.CM.Profiles[name]
		profiles = append(profiles, CMProfile{Name: name, BaseURL: profile.BaseURL, Model: profile.Model})
	}
	return CMProfileList{DefaultProfile: document.CM.DefaultProfile, Profiles: profiles}, nil
}

// AddCMProfile silently replaces an existing profile while retaining the default selection.
func (store *Store) AddCMProfile(name, baseURL, model, apiKey string) error {
	return store.updateDocument(func(document *document) error {
		cm := ensureCMDocument(document)
		key, err := store.keyForSalt(document.Salt)
		if err != nil {
			return err
		}
		encryptedAPIKey, err := encryptValue(apiKey, key, store.random)
		if err != nil {
			return err
		}
		if _, exists := cm.Profiles[name]; !exists {
			cm.order = appendOrderedName(cm.order, name)
		}
		cm.Profiles[name] = cmDocumentProfile{
			BaseURL: normalizeCMBaseURL(baseURL),
			Model:   strings.TrimSpace(model),
			APIKey:  encryptedAPIKey,
		}
		if cm.DefaultProfile == "" {
			cm.DefaultProfile = name
		}
		return nil
	})
}

// RemoveCMProfile removes one profile and updates the default when needed.
func (store *Store) RemoveCMProfile(name string) (bool, error) {
	removed := false
	err := store.updateDocument(func(document *document) error {
		cm := ensureCMDocument(document)
		if _, exists := cm.Profiles[name]; !exists {
			return nil
		}
		delete(cm.Profiles, name)
		cm.order = removeOrderedName(cm.order, name)
		if cm.DefaultProfile == name {
			order := normalizedOrder(cm.order, cm.Profiles)
			if len(order) == 0 {
				cm.DefaultProfile = ""
			} else {
				cm.DefaultProfile = order[0]
			}
		}
		removed = true
		return nil
	})
	return removed, err
}

// SetDefaultCMProfile selects an existing stored profile.
func (store *Store) SetDefaultCMProfile(name string) error {
	return store.updateDocument(func(document *document) error {
		cm := ensureCMDocument(document)
		if _, exists := cm.Profiles[name]; !exists {
			return fmt.Errorf("CM profile not found: %s", name)
		}
		cm.DefaultProfile = name
		return nil
	})
}

// SetCMProfileValue applies the legacy semantic update for one supported key.
func (store *Store) SetCMProfileValue(name, key, value string) error {
	return store.updateDocument(func(document *document) error {
		cm := ensureCMDocument(document)
		profile, exists := cm.Profiles[name]
		if !exists {
			return fmt.Errorf("CM profile not found: %s", name)
		}
		switch key {
		case "baseURL":
			profile.BaseURL = normalizeCMBaseURL(value)
		case "model":
			profile.Model = strings.TrimSpace(value)
		case "apiKey":
			cryptoKey, err := store.keyForSalt(document.Salt)
			if err != nil {
				return err
			}
			encryptedAPIKey, err := encryptValue(value, cryptoKey, store.random)
			if err != nil {
				return err
			}
			profile.APIKey = encryptedAPIKey
		case "temperature":
			temperature, ok := parseFiniteJavaScriptNumber(value)
			if !ok || temperature < 0 || temperature > 2 {
				return errors.New("temperature must be a number between 0 and 2")
			}
			profile.Temperature = &temperature
		case "timeoutMs":
			timeout, ok := parseIntegerPrefix(value)
			if !ok || timeout < 1000 {
				return errors.New("timeoutMs must be an integer greater than or equal to 1000")
			}
			profile.TimeoutMS = &timeout
		case "maxOutputTokens":
			maximum, ok := parseIntegerPrefix(value)
			if !ok || maximum < 32 {
				return errors.New("maxOutputTokens must be an integer greater than or equal to 32")
			}
			profile.MaxOutputTokens = &maximum
		default:
			return errors.New("Unsupported key. Use baseURL, model, apiKey, temperature, timeoutMs, or maxOutputTokens.")
		}
		cm.Profiles[name] = profile
		return nil
	})
}

// ResolveCMProfile selects, decrypts, and normalizes one profile for its provider consumer.
func (store *Store) ResolveCMProfile(options CMResolveOptions) (ResolvedCMProfile, error) {
	document, _, err := store.readDocument()
	if err != nil {
		return ResolvedCMProfile{}, err
	}
	cm := document.CM
	if cm == nil {
		cm = &cmDocument{Profiles: map[string]cmDocumentProfile{}}
	}
	selectedName := firstNonEmpty(options.ProfileName, store.environment("YCY_CM_PROFILE"), cm.DefaultProfile)
	if selectedName == "" {
		order := normalizedOrder(cm.order, cm.Profiles)
		if len(order) > 0 {
			selectedName = order[0]
		}
	}
	stored, hasStored := cm.Profiles[selectedName]

	baseURL := firstNonEmpty(store.environment("YCY_CM_BASE_URL"), stored.BaseURL)
	model := firstNonEmpty(store.environment("YCY_CM_MODEL"), stored.Model)
	apiKey := store.environment("YCY_CM_API_KEY")
	if apiKey == "" && hasStored && stored.APIKey != "" {
		cryptoKey, err := store.keyForSalt(document.Salt)
		if err != nil {
			return ResolvedCMProfile{}, err
		}
		apiKey, err = decryptValue(stored.APIKey, cryptoKey)
		if err != nil {
			return ResolvedCMProfile{}, err
		}
	}
	if baseURL == "" || model == "" || apiKey == "" {
		return ResolvedCMProfile{}, errors.New("No usable CM profile found. Run \"ycy config cm add\" or set YCY_CM_BASE_URL, YCY_CM_MODEL, and YCY_CM_API_KEY.")
	}

	temperature := defaultCMTemperature
	timeout := defaultCMTimeoutMS
	maximum := defaultCMMaxOutputTokens
	if hasStored {
		if stored.Temperature != nil {
			temperature = *stored.Temperature
		}
		if stored.TimeoutMS != nil {
			timeout = float64(*stored.TimeoutMS)
		}
		if stored.MaxOutputTokens != nil {
			maximum = float64(*stored.MaxOutputTokens)
		}
	}
	if value, ok := parseCMEnvironmentNumber(store.environment("YCY_CM_TEMPERATURE")); ok {
		temperature = value
	}
	if value, ok := parseCMEnvironmentNumber(store.environment("YCY_CM_TIMEOUT_MS")); ok {
		timeout = value
	}
	if value, ok := parseCMEnvironmentNumber(store.environment("YCY_CM_MAX_OUTPUT_TOKENS")); ok {
		maximum = value
	}
	if options.TimeoutOverrideMS != nil {
		timeout = *options.TimeoutOverrideMS
	}
	if selectedName == "" {
		selectedName = "env"
	}
	return ResolvedCMProfile{
		Name:            selectedName,
		BaseURL:         normalizeCMBaseURL(baseURL),
		Model:           model,
		APIKey:          apiKey,
		Temperature:     temperature,
		TimeoutMS:       timeout,
		MaxOutputTokens: maximum,
	}, nil
}

func ensureCMDocument(document *document) *cmDocument {
	if document.CM == nil {
		document.CM = &cmDocument{Profiles: map[string]cmDocumentProfile{}}
	}
	if document.CM.Profiles == nil {
		document.CM.Profiles = map[string]cmDocumentProfile{}
	}
	return document.CM
}

func normalizeCMBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseCMEnvironmentNumber(value string) (float64, bool) {
	if value == "" {
		return 0, false
	}
	return parseFiniteJavaScriptNumber(value)
}

func parseFiniteJavaScriptNumber(value string) (float64, bool) {
	trimmed := trimJavaScriptWhitespace(value)
	if trimmed == "" {
		return 0, true
	}
	if len(trimmed) > 2 && trimmed[0] == '0' {
		base := 0
		switch trimmed[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			integer, err := strconv.ParseUint(trimmed[2:], base, 64)
			if err == nil {
				return float64(integer), true
			}
			return 0, false
		}
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
		return parsed, true
	}
	return 0, false
}

func parseIntegerPrefix(value string) (int, bool) {
	trimmed := trimJavaScriptWhitespace(value)
	if trimmed == "" {
		return 0, false
	}
	index := 0
	if trimmed[index] == '+' || trimmed[index] == '-' {
		index++
	}
	start := index
	for index < len(trimmed) && trimmed[index] >= '0' && trimmed[index] <= '9' {
		index++
	}
	if index == start {
		return 0, false
	}
	parsed, err := strconv.ParseInt(trimmed[:index], 10, 0)
	if err != nil {
		return 0, false
	}
	return int(parsed), true
}

func trimJavaScriptWhitespace(value string) string {
	return strings.TrimFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) || character == '\uFEFF'
	})
}

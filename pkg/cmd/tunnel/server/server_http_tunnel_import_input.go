package server

import (
	"encoding/json"
	"errors"
)

const serverTunnelImportSourceLimit = 1024 * 1024

type serverHTTPTunnelImportPreviewInput struct {
	Source string
}

func (input *serverHTTPTunnelImportPreviewInput) UnmarshalJSON(source []byte) error {
	object, err := serverTunnelJSONObject(source, "source")
	if err != nil {
		return err
	}
	value, err := serverTunnelImportSource(object)
	if err != nil {
		return err
	}
	input.Source = value
	return nil
}

type serverHTTPTunnelImportInput struct {
	Source       string
	CandidateIDs []string
}

func (input *serverHTTPTunnelImportInput) UnmarshalJSON(source []byte) error {
	object, err := serverTunnelJSONObject(source, "source", "candidateIds")
	if err != nil {
		return err
	}
	value, err := serverTunnelImportSource(object)
	if err != nil {
		return err
	}
	candidateIDs, err := serverTunnelImportCandidateIDs(object)
	if err != nil {
		return err
	}
	input.Source = value
	input.CandidateIDs = candidateIDs
	return nil
}

func serverTunnelImportSource(object map[string]json.RawMessage) (string, error) {
	value, err := serverTunnelRequiredString(object, "source")
	if err != nil {
		return "", err
	}
	if value == "" || utf16CodeUnitCount(value) > serverTunnelImportSourceLimit {
		return "", errors.New("source must contain at most one MiB of text")
	}
	return value, nil
}

func serverTunnelImportCandidateIDs(object map[string]json.RawMessage) ([]string, error) {
	raw, found := object["candidateIds"]
	if !found || serverTunnelNull(raw) {
		return nil, errors.New("candidateIds must contain at least one value")
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("candidateIds must contain at least one value")
	}
	return values, nil
}

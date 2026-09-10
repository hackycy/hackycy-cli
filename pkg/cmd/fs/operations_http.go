package fs

import (
	"encoding/json"
	"io"
)

func decodeOperation(body io.Reader) (Operation, error) {
	decoder := json.NewDecoder(io.LimitReader(body, 1<<20))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return Operation{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Operation{}, io.ErrUnexpectedEOF
	}
	action, ok := operationString(fields, "action")
	if !ok {
		return Operation{}, io.ErrUnexpectedEOF
	}
	operation := Operation{Action: action}
	switch action {
	case "create-directory":
		if !operationOnlyFields(fields, "action", "parentPath", "name") {
			return Operation{}, io.ErrUnexpectedEOF
		}
		var parentPath, name string
		if parentPath, ok = operationString(fields, "parentPath"); !ok {
			return Operation{}, io.ErrUnexpectedEOF
		}
		if name, ok = operationString(fields, "name"); !ok {
			return Operation{}, io.ErrUnexpectedEOF
		}
		operation.ParentPath, operation.Name = parentPath, name
	case "rename":
		if !operationOnlyFields(fields, "action", "path", "newName") {
			return Operation{}, io.ErrUnexpectedEOF
		}
		var path, name string
		if path, ok = operationString(fields, "path"); !ok {
			return Operation{}, io.ErrUnexpectedEOF
		}
		if name, ok = operationString(fields, "newName"); !ok {
			return Operation{}, io.ErrUnexpectedEOF
		}
		operation.Path, operation.NewName = path, name
	case "copy", "move":
		if !operationOnlyFields(fields, "action", "paths", "destinationPath") {
			return Operation{}, io.ErrUnexpectedEOF
		}
		paths, ok := operationStrings(fields, "paths")
		if !ok || len(paths) == 0 || len(paths) > 1000 {
			return Operation{}, io.ErrUnexpectedEOF
		}
		destination, ok := operationString(fields, "destinationPath")
		if !ok {
			return Operation{}, io.ErrUnexpectedEOF
		}
		operation.Paths, operation.DestinationPath = paths, destination
	case "delete":
		if !operationOnlyFields(fields, "action", "paths") {
			return Operation{}, io.ErrUnexpectedEOF
		}
		paths, ok := operationStrings(fields, "paths")
		if !ok || len(paths) == 0 || len(paths) > 1000 {
			return Operation{}, io.ErrUnexpectedEOF
		}
		operation.Paths = paths
	default:
		return Operation{}, io.ErrUnexpectedEOF
	}
	if !operationWithinLimit(operation) {
		return Operation{}, io.ErrUnexpectedEOF
	}
	return operation, nil
}

func operationString(fields map[string]json.RawMessage, key string) (string, bool) {
	value, found := fields[key]
	if !found {
		return "", false
	}
	var decoded string
	return decoded, json.Unmarshal(value, &decoded) == nil
}

func operationStrings(fields map[string]json.RawMessage, key string) ([]string, bool) {
	value, found := fields[key]
	if !found {
		return nil, false
	}
	var decoded []string
	return decoded, json.Unmarshal(value, &decoded) == nil
}

func operationOnlyFields(fields map[string]json.RawMessage, allowed ...string) bool {
	if len(fields) != len(allowed) {
		return false
	}
	for _, key := range allowed {
		if _, found := fields[key]; !found {
			return false
		}
	}
	return true
}

func operationWithinLimit(operation Operation) bool {
	for _, value := range []string{operation.ParentPath, operation.Name, operation.Path, operation.NewName, operation.DestinationPath} {
		if len(value) > 4096 {
			return false
		}
	}
	for _, value := range operation.Paths {
		if len(value) > 4096 {
			return false
		}
	}
	return true
}

package policy

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/psyb0t/ctxerrors"
)

func compileNoulCriteria(id string, raw any) (map[string]any, error) {
	if raw == nil {
		return nil, nil //nolint:nilnil // absent Noul criteria is valid
	}

	source, err := noulCriteriaMap(raw)
	if err != nil {
		return nil, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"noul question %q criteria must be a true/false map",
			id,
		)
	}

	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	criteria := make(map[string]any, len(source))

	for _, key := range keys {
		if key != "true" && key != "false" {
			return nil, ctxerrors.Wrapf(
				ErrInvalidQuestion,
				"noul question %q has unsupported criteria key %q",
				id,
				key,
			)
		}

		value, err := compileCriteriaValue(id, key, source[key], true)
		if err != nil {
			return nil, err
		}

		criteria[key] = value
	}

	return criteria, nil
}

func noulCriteriaMap(raw any) (map[string]any, error) {
	if source, ok := raw.(map[string]any); ok {
		return source, nil
	}

	source, ok := raw.(map[any]any)
	if !ok {
		return nil, ctxerrors.New("criteria is not an object")
	}

	criteria := make(map[string]any, len(source))
	for key, value := range source {
		switch typed := key.(type) {
		case string:
			criteria[typed] = value
		case bool:
			if typed {
				criteria["true"] = value
			} else {
				criteria["false"] = value
			}
		default:
			return nil, ctxerrors.New("criteria has a non-string key")
		}
	}

	return criteria, nil
}

func compileCriteriaValue(
	id string,
	name string,
	raw any,
	allowNull bool,
) (any, error) {
	value, err := normalizeQuestionValue(raw, allowNull)
	if err != nil {
		return nil, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"question %q criteria %q is invalid: %v",
			id,
			name,
			err,
		)
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"question %q criteria %q cannot be encoded",
			id,
			name,
		)
	}

	if len(encoded) > MaxCriteriaTextLength {
		return nil, ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"question %q criteria %q exceeds %d encoded bytes",
			id,
			name,
			MaxCriteriaTextLength,
		)
	}

	return value, nil
}

func normalizeQuestionValue(raw any, allowNull bool) (any, error) {
	if raw == nil {
		if allowNull {
			return nil, nil //nolint:nilnil // a null criterion is valid
		}

		return nil, ctxerrors.New("null is not allowed")
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "encode structured question value")
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()

	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, ctxerrors.Wrap(err, "decode structured question value")
	}

	switch normalized.(type) {
	case string, map[string]any, []any:
	default:
		return nil, ctxerrors.New("value must be a string, object, or array")
	}

	nodes := 0
	if err := validateQuestionValue(normalized, 1, &nodes); err != nil {
		return nil, err
	}

	return normalized, nil
}

func validateQuestionValue(value any, depth int, nodes *int) error {
	(*nodes)++
	if *nodes > MaxQuestionValueNodes {
		return ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"structured question value exceeds %d nodes",
			MaxQuestionValueNodes,
		)
	}

	if depth > MaxQuestionValueDepth {
		return ctxerrors.Wrapf(
			ErrInvalidQuestion,
			"structured question value exceeds depth %d",
			MaxQuestionValueDepth,
		)
	}

	switch typed := value.(type) {
	case nil, string, bool, json.Number:
		return nil
	case []any:
		return validateQuestionChildren(typed, depth, nodes)
	case map[string]any:
		children := make([]any, 0, len(typed))
		for _, child := range typed {
			children = append(children, child)
		}

		return validateQuestionChildren(children, depth, nodes)
	default:
		return ctxerrors.New(
			"structured question value contains an unsupported value",
		)
	}
}

func validateQuestionChildren(children []any, depth int, nodes *int) error {
	for _, child := range children {
		if err := validateQuestionValue(child, depth+1, nodes); err != nil {
			return err
		}
	}

	return nil
}
